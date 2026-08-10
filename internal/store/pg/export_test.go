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
// cascade to keys, runs and codes.
func (r *Repo) Truncate(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `truncate users, user_keys, runs, link_codes restart identity cascade`)
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
