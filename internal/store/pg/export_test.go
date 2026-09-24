package pg

import (
	"context"
	"time"

	"github.com/Pikaryu729/typesafe/internal/store"
)

// Helpers the integration tests need and production code must not have. They
// live here because export_test.go is compiled only under `go test`, so
// nothing outside a test run can reach them.

// Truncate empties every table, so each test starts from nothing. Accounts
// cascade to keys, runs, codes and purchases; the tables are still listed so
// adding one that does not cascade cannot silently leak state between tests.
func (r *Repo) Truncate(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		truncate users, user_keys, runs, purchases, link_codes restart identity cascade`)
	return err
}

// CreateExpiredLinkCode issues a code that is already past its expiry, so the
// expiry path can be tested without waiting out the TTL.
func (r *Repo) CreateExpiredLinkCode(ctx context.Context, userID string) (string, error) {
	code := store.NewLinkCode()
	_, err := r.pool.Exec(ctx, `
		insert into link_codes (code, user_id, expires_at) values ($1, $2, $3)`,
		code, userID, time.Now().Add(-time.Minute))
	return code, err
}

func (r *Repo) HoldAccountLock(ctx context.Context, userID string) (func(), error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, userID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return func() { _ = tx.Commit(context.Background()) }, nil
}
