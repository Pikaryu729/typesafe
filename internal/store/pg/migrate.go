package pg

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLockID names the advisory lock migrations are applied under. The
// value is arbitrary but must never change: it is what stops two servers
// started at once from applying the same migration twice.
const migrationLockID = 8250734

// Migrate applies every migration the database has not already seen, in
// filename order, each in its own transaction.
//
// There is no migration library here on purpose. The whole mechanism is a
// version table, a sorted directory and a lock, and a dependency would be more
// code to audit than the thing it replaces.
func (r *Repo) Migrate(ctx context.Context) error {
	files, err := migrationFiles()
	if err != nil {
		return err
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	// Held for the whole run, on one connection, so a second process waits
	// here rather than racing us. Released when the connection goes back.
	if _, err := conn.Exec(ctx, `select pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `select pg_advisory_unlock($1)`, migrationLockID)
	}()

	if _, err := conn.Exec(ctx, `
		create table if not exists schema_migrations (
			version    integer primary key,
			applied_at timestamptz not null default now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	for _, f := range files {
		if slices.Contains(applied, f.version) {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + f.name)
		if err != nil {
			return fmt.Errorf("read %s: %w", f.name, err)
		}
		if err := applyMigration(ctx, conn, f, string(body)); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration and records it, together, so a failure
// halfway cannot leave the version table claiming success.
func applyMigration(ctx context.Context, conn pgxConn, f migrationFile, body string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", f.name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("apply %s: %w", f.name, err)
	}
	if _, err := tx.Exec(ctx, `insert into schema_migrations (version) values ($1)`, f.version); err != nil {
		return fmt.Errorf("record %s: %w", f.name, err)
	}
	return tx.Commit(ctx)
}

// pgxConn is the slice of a pooled connection this file needs, so the helpers
// above can be given either a pool connection or a plain one.
type pgxConn interface {
	Begin(context.Context) (pgx.Tx, error)
}

func appliedVersions(ctx context.Context, conn interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
},
) ([]int, error) {
	rows, err := conn.Query(ctx, `select version from schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type migrationFile struct {
	version int
	name    string
}

// migrationFiles lists the embedded migrations in version order. Names are
// NNNN_description.sql; the leading number is the version.
func migrationFiles() ([]migrationFile, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	out := make([]migrationFile, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %q is not named NNNN_description.sql", name)
		}
		v, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q has a non-numeric version: %w", name, err)
		}
		out = append(out, migrationFile{version: v, name: name})
	}

	slices.SortFunc(out, func(a, b migrationFile) int { return a.version - b.version })
	return out, nil
}
