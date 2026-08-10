// Package pg implements store.Repository on PostgreSQL.
//
// The schema and the queries are written by hand: there is no ORM and no
// migration library. At this size both would be more code to understand than
// the SQL they hide.
package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Pikaryu729/typesafe/internal/store"
)

// maxConns caps the pool. Shared-core Cloud SQL instances allow only a few
// dozen connections in total, and one typing server needs very few: writes are
// serialised behind a single async worker and reads happen when someone opens
// their profile.
const maxConns = 4

// connectTimeout bounds the initial dial, so a misconfigured DSN fails the
// server's startup quickly instead of hanging it.
const connectTimeout = 10 * time.Second

// Repo is a Postgres-backed store.Repository.
type Repo struct {
	pool *pgxpool.Pool
}

// New connects to dsn and verifies the connection. The caller should call
// Migrate before using the repository.
func New(ctx context.Context, dsn string) (*Repo, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = maxConns

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Repo{pool: pool}, nil
}

// Close releases the pool.
func (r *Repo) Close() { r.pool.Close() }

const userColumns = `id, display_name, created_at, last_seen_at`

func (r *Repo) ResolveUser(ctx context.Context, fingerprint, connectedName string) (store.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return store.User{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Two sessions can present the same unseen key at the same moment — the
	// same person opening two terminals. Serialise on the fingerprint so the
	// check-then-insert below cannot produce two accounts for one key.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, fingerprint); err != nil {
		return store.User{}, fmt.Errorf("lock fingerprint: %w", err)
	}

	// Known key: touch last_seen_at and return the account in one statement.
	u, err := scanUser(tx.QueryRow(ctx, `
		update users set last_seen_at = now()
		where id = (select user_id from user_keys where fingerprint = $1)
		returning `+userColumns, fingerprint))
	switch {
	case err == nil:
		return u, tx.Commit(ctx)
	case !errors.Is(err, pgx.ErrNoRows):
		return store.User{}, fmt.Errorf("resolve user: %w", err)
	}

	// Unknown key: a new account, named for whatever they typed at the prompt.
	u, err = scanUser(tx.QueryRow(ctx, `
		insert into users (id, display_name) values ($1, $2)
		returning `+userColumns, store.NewID(), connectedName))
	if err != nil {
		return store.User{}, fmt.Errorf("create user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into user_keys (fingerprint, user_id) values ($1, $2)`, fingerprint, u.ID); err != nil {
		return store.User{}, fmt.Errorf("attach key: %w", err)
	}
	return u, tx.Commit(ctx)
}

func (r *Repo) RecordRun(ctx context.Context, run store.Run) error {
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	_, err := r.pool.Exec(ctx, `
		insert into runs (
			user_id, mode, seed, word_count, wpm, raw_wpm, accuracy,
			duration_ms, keystrokes, correct, incorrect, race_code, place, created_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		run.UserID, string(run.Mode), run.Seed, run.WordCount, run.WPM, run.RawWPM,
		run.Accuracy, run.Duration.Milliseconds(), run.Keystrokes, run.Correct,
		run.Incorrect, nullString(run.RaceCode), nullInt(run.Place), run.CreatedAt)
	if err != nil {
		return fmt.Errorf("record run: %w", err)
	}
	return nil
}

func (r *Repo) RecentRuns(ctx context.Context, userID string, limit int) ([]store.Run, error) {
	// id breaks ties so the order is stable when two runs share a timestamp.
	rows, err := r.pool.Query(ctx, `
		select mode, seed, word_count, wpm, raw_wpm, accuracy, duration_ms,
		       keystrokes, correct, incorrect, coalesce(race_code, ''),
		       coalesce(place, 0), created_at
		from runs where user_id = $1
		order by created_at desc, id desc
		limit $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("recent runs: %w", err)
	}
	defer rows.Close()

	var out []store.Run
	for rows.Next() {
		var (
			run        store.Run
			mode       string
			durationMS int64
		)
		if err := rows.Scan(&mode, &run.Seed, &run.WordCount, &run.WPM, &run.RawWPM,
			&run.Accuracy, &durationMS, &run.Keystrokes, &run.Correct, &run.Incorrect,
			&run.RaceCode, &run.Place, &run.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		run.UserID = userID
		run.Mode = store.Mode(mode)
		run.Duration = time.Duration(durationMS) * time.Millisecond
		out = append(out, run)
	}
	return out, rows.Err()
}

// Summary aggregates in SQL rather than pulling the history down. The figures
// must match store.Summarize exactly — the integration tests compare the two
// against identical input, because that agreement is easy to break silently.
func (r *Repo) Summary(ctx context.Context, userID string) (store.Summary, error) {
	var (
		s          store.Summary
		totalMS    int64
		recentWPM  float64
		bestAccura float64
	)
	err := r.pool.QueryRow(ctx, `
		with mine as (
			select * from runs where user_id = $1
		),
		best as (
			select accuracy from mine order by wpm desc, created_at desc, id desc limit 1
		),
		recent as (
			select wpm from mine order by created_at desc, id desc limit $2
		)
		select
			(select count(*) from mine),
			(select count(*) from mine where mode = 'race'),
			(select count(*) from mine where mode = 'race' and place = 1),
			coalesce((select max(wpm) from mine), 0),
			coalesce((select accuracy from best), 0),
			coalesce((select avg(wpm) from recent), 0),
			coalesce((select sum(duration_ms) from mine), 0)`,
		userID, store.RecentWindow).
		Scan(&s.Runs, &s.Races, &s.Wins, &s.BestWPM, &bestAccura, &recentWPM, &totalMS)
	if err != nil {
		return store.Summary{}, fmt.Errorf("summary: %w", err)
	}

	s.BestAccuracy = bestAccura
	s.RecentWPM = recentWPM
	s.TotalTime = time.Duration(totalMS) * time.Millisecond
	return s, nil
}

func (r *Repo) CreateLinkCode(ctx context.Context, userID string) (store.LinkCode, error) {
	// Housekeeping while we are here; there is no other sweeper and the table
	// would otherwise grow forever with codes nobody used.
	if _, err := r.pool.Exec(ctx, `delete from link_codes where expires_at < now()`); err != nil {
		return store.LinkCode{}, fmt.Errorf("expire old codes: %w", err)
	}

	expires := time.Now().Add(store.LinkCodeTTL)
	// A collision is vanishingly unlikely but trivially recoverable, so retry
	// rather than hand the user an error they cannot act on.
	for range 5 {
		code := store.NewLinkCode()
		_, err := r.pool.Exec(ctx, `
			insert into link_codes (code, user_id, expires_at) values ($1, $2, $3)`,
			code, userID, expires)
		if err == nil {
			return store.LinkCode{Code: code, ExpiresAt: expires}, nil
		}
		if !isUniqueViolation(err) {
			return store.LinkCode{}, fmt.Errorf("create link code: %w", err)
		}
	}
	return store.LinkCode{}, errors.New("pg: could not find a free link code")
}

func (r *Repo) RedeemLinkCode(ctx context.Context, code, fingerprint string) (store.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return store.User{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Deleting on read is what makes a code single-use even if two sessions
	// redeem it at the same instant: only one delete returns a row.
	var (
		targetID string
		expires  time.Time
	)
	err = tx.QueryRow(ctx, `
		delete from link_codes where code = $1 returning user_id, expires_at`, code).
		Scan(&targetID, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.User{}, store.ErrUnknownCode
	}
	if err != nil {
		return store.User{}, fmt.Errorf("redeem: %w", err)
	}
	if time.Now().After(expires) {
		// Consumed anyway: an expired code should not linger to be tried again.
		return store.User{}, errors.Join(store.ErrExpiredCode, tx.Commit(ctx))
	}

	var sourceID string
	err = tx.QueryRow(ctx, `select user_id from user_keys where fingerprint = $1`, fingerprint).Scan(&sourceID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// A key with no account yet. Just point it at the target.
		if _, err := tx.Exec(ctx, `
			insert into user_keys (fingerprint, user_id) values ($1, $2)`, fingerprint, targetID); err != nil {
			return store.User{}, fmt.Errorf("attach key: %w", err)
		}
	case err != nil:
		return store.User{}, fmt.Errorf("find current account: %w", err)
	case sourceID != targetID:
		// Fold the whole source account into the target — its runs and every
		// other key that reached it — then drop the empty account. Order
		// matters: the rows have to move before the cascade could take them.
		if _, err := tx.Exec(ctx, `update runs set user_id = $1 where user_id = $2`, targetID, sourceID); err != nil {
			return store.User{}, fmt.Errorf("move runs: %w", err)
		}
		if _, err := tx.Exec(ctx, `update user_keys set user_id = $1 where user_id = $2`, targetID, sourceID); err != nil {
			return store.User{}, fmt.Errorf("move keys: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from users where id = $1`, sourceID); err != nil {
			return store.User{}, fmt.Errorf("delete merged account: %w", err)
		}
	}

	u, err := scanUser(tx.QueryRow(ctx, `select `+userColumns+` from users where id = $1`, targetID))
	if err != nil {
		return store.User{}, fmt.Errorf("load account: %w", err)
	}
	return u, tx.Commit(ctx)
}

func scanUser(row pgx.Row) (store.User, error) {
	var u store.User
	err := row.Scan(&u.ID, &u.DisplayName, &u.CreatedAt, &u.LastSeenAt)
	return u, err
}

// nullString and nullInt keep "not applicable" out of the data as NULL rather
// than as an empty string or a zero place, which would read as real values.

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullInt(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}

// isUniqueViolation reports whether err is a primary key or unique constraint
// conflict (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}
