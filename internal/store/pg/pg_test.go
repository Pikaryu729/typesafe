//go:build integration

// Integration tests for the Postgres repository. They need a real database:
//
//	TYPESAFE_TEST_DSN=postgres://user:pass@localhost:5432/typesafe_test \
//	    go test -tags integration ./internal/store/pg
//
// CI provides one as a service container. Without the variable set, everything
// here skips, so the default `go test ./...` stays database-free.
package pg_test

import (
	"context"
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"github.com/Pikaryu729/typesafe/internal/store"
	"github.com/Pikaryu729/typesafe/internal/store/pg"
)

func newRepo(t *testing.T) *pg.Repo {
	t.Helper()

	dsn := os.Getenv("TYPESAFE_TEST_DSN")
	if dsn == "" {
		t.Skip("TYPESAFE_TEST_DSN is not set")
	}

	ctx := context.Background()
	repo, err := pg.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(repo.Close)

	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Every test starts from an empty database; the accounts cascade to
	// everything else.
	if err := repo.Truncate(ctx); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return repo
}

func TestMigrateIsIdempotent(t *testing.T) {
	repo := newRepo(t)
	// newRepo has already migrated once; a second pass must be a no-op rather
	// than an error about tables that already exist.
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestResolveUserCreatesOnceAndReusesTheKey(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	first, err := repo.ResolveUser(ctx, "SHA256:aaa", "ryu")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	again, err := repo.ResolveUser(ctx, "SHA256:aaa", "ignored")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if again.ID != first.ID {
		t.Errorf("second resolve made a new account: %q then %q", first.ID, again.ID)
	}
	if again.DisplayName != "ryu" {
		t.Errorf("display name = %q; the name typed at the prompt must not overwrite it", again.DisplayName)
	}
	if !again.LastSeenAt.After(first.LastSeenAt) && !again.LastSeenAt.Equal(first.LastSeenAt) {
		t.Error("last_seen_at went backwards")
	}
}

func TestConcurrentFirstConnectCreatesOneAccount(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	// The same person opening several terminals at once, all presenting a key
	// the server has never seen.
	const n = 6
	ids := make(chan string, n)
	errs := make(chan error, n)
	for range n {
		go func() {
			u, err := repo.ResolveUser(ctx, "SHA256:racy", "ryu")
			ids <- u.ID
			errs <- err
		}()
	}

	var first string
	for range n {
		if err := <-errs; err != nil {
			t.Fatalf("ResolveUser: %v", err)
		}
		id := <-ids
		if first == "" {
			first = id
		} else if id != first {
			t.Fatalf("one key produced two accounts: %q and %q", first, id)
		}
	}
}

func TestRunsRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u, _ := repo.ResolveUser(ctx, "SHA256:aaa", "ryu")

	want := store.Run{
		UserID:     u.ID,
		Mode:       store.ModeRace,
		Seed:       1234567890,
		WordCount:  30,
		WPM:        82.5,
		RawWPM:     88.25,
		Accuracy:   0.937,
		Duration:   31500 * time.Millisecond,
		Keystrokes: 173,
		Correct:    162,
		Incorrect:  11,
		RaceCode:   "QK4T",
		Place:      2,
		CreatedAt:  time.Now().Truncate(time.Millisecond),
	}
	if err := repo.RecordRun(ctx, want); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	got, err := repo.RecentRuns(ctx, u.ID, 10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d runs, want 1", len(got))
	}

	g := got[0]
	if !g.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", g.CreatedAt, want.CreatedAt)
	}
	g.CreatedAt, want.CreatedAt = time.Time{}, time.Time{}
	if g != want {
		t.Errorf("round trip changed the run:\n got %+v\nwant %+v", g, want)
	}
}

func TestPracticeRunKeepsNullRaceColumns(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u, _ := repo.ResolveUser(ctx, "SHA256:aaa", "ryu")

	if err := repo.RecordRun(ctx, store.Run{
		UserID: u.ID, Mode: store.ModePractice, Seed: 7, WordCount: 30,
		WPM: 70, RawWPM: 72, Accuracy: 0.97, Duration: time.Minute, Keystrokes: 150,
	}); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	got, _ := repo.RecentRuns(ctx, u.ID, 1)
	if got[0].RaceCode != "" || got[0].Place != 0 {
		t.Errorf("practice run came back with race data: code %q place %d", got[0].RaceCode, got[0].Place)
	}
}

func TestRecentRunsAreNewestFirst(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u, _ := repo.ResolveUser(ctx, "SHA256:aaa", "ryu")

	base := time.Now().Add(-time.Hour)
	for i := range 5 {
		if err := repo.RecordRun(ctx, store.Run{
			UserID: u.ID, Mode: store.ModePractice, WPM: float64(60 + i),
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("RecordRun: %v", err)
		}
	}

	got, _ := repo.RecentRuns(ctx, u.ID, 3)
	if len(got) != 3 {
		t.Fatalf("got %d runs, want 3", len(got))
	}
	if got[0].WPM != 64 {
		t.Errorf("newest run has %v wpm, want 64", got[0].WPM)
	}
}

// TestSummaryMatchesSummarize is the point of this file: the SQL aggregation
// and store.Summarize are two implementations of one definition, and nothing
// but this test stops them drifting apart.
func TestSummaryMatchesSummarize(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u, _ := repo.ResolveUser(ctx, "SHA256:aaa", "ryu")

	base := time.Now().Add(-24 * time.Hour)
	var runs []store.Run
	for i := range store.RecentWindow + 5 {
		r := store.Run{
			UserID:    u.ID,
			Mode:      store.ModePractice,
			Seed:      int64(i),
			WordCount: 30,
			WPM:       float64(50 + i*3),
			RawWPM:    float64(55 + i*3),
			Accuracy:  0.9 + float64(i%5)/100,
			Duration:  time.Duration(30+i) * time.Second,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if i%3 == 0 {
			r.Mode = store.ModeRace
			r.RaceCode = "ABCD"
			r.Place = 1 + i%3
		}
		runs = append(runs, r)
		if err := repo.RecordRun(ctx, r); err != nil {
			t.Fatalf("RecordRun: %v", err)
		}
	}

	got, err := repo.Summary(ctx, u.ID)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}

	// Summarize expects newest first, which is the order RecentRuns returns.
	newestFirst, _ := repo.RecentRuns(ctx, u.ID, len(runs))
	want := store.Summarize(newestFirst)

	if got.Runs != want.Runs || got.Races != want.Races || got.Wins != want.Wins {
		t.Errorf("counts: got %+v, want %+v", got, want)
	}
	if !closeEnough(got.BestWPM, want.BestWPM) {
		t.Errorf("BestWPM = %v, want %v", got.BestWPM, want.BestWPM)
	}
	if !closeEnough(got.BestAccuracy, want.BestAccuracy) {
		t.Errorf("BestAccuracy = %v, want %v", got.BestAccuracy, want.BestAccuracy)
	}
	if !closeEnough(got.RecentWPM, want.RecentWPM) {
		t.Errorf("RecentWPM = %v, want %v", got.RecentWPM, want.RecentWPM)
	}
	if got.TotalTime != want.TotalTime {
		t.Errorf("TotalTime = %v, want %v", got.TotalTime, want.TotalTime)
	}
}

func TestSummaryOfAnEmptyAccount(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u, _ := repo.ResolveUser(ctx, "SHA256:aaa", "ryu")

	got, err := repo.Summary(ctx, u.ID)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got != (store.Summary{}) {
		t.Errorf("got %+v, want the zero Summary", got)
	}
}

func TestLinkCodeMergesTheTwoAccounts(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	laptop, _ := repo.ResolveUser(ctx, "SHA256:laptop", "ryu")
	desktop, _ := repo.ResolveUser(ctx, "SHA256:desktop", "ryu")
	mustRecord(t, repo, laptop.ID, 80)
	mustRecord(t, repo, desktop.ID, 70)

	code, err := repo.CreateLinkCode(ctx, laptop.ID)
	if err != nil {
		t.Fatalf("CreateLinkCode: %v", err)
	}

	got, err := repo.RedeemLinkCode(ctx, code.Code, "SHA256:desktop")
	if err != nil {
		t.Fatalf("RedeemLinkCode: %v", err)
	}
	if got.ID != laptop.ID {
		t.Fatalf("redeemed into %q, want %q", got.ID, laptop.ID)
	}

	back, _ := repo.ResolveUser(ctx, "SHA256:desktop", "ryu")
	if back.ID != laptop.ID {
		t.Errorf("desktop key resolves to %q, want %q", back.ID, laptop.ID)
	}
	runs, _ := repo.RecentRuns(ctx, laptop.ID, 10)
	if len(runs) != 2 {
		t.Errorf("merged account has %d runs, want 2 — history was lost", len(runs))
	}
}

func TestLinkCodeIsSingleUseAndExpires(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	u, _ := repo.ResolveUser(ctx, "SHA256:aaa", "ryu")

	code, _ := repo.CreateLinkCode(ctx, u.ID)
	if _, err := repo.RedeemLinkCode(ctx, code.Code, "SHA256:bbb"); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if _, err := repo.RedeemLinkCode(ctx, code.Code, "SHA256:ccc"); !errors.Is(err, store.ErrUnknownCode) {
		t.Errorf("second redeem = %v, want ErrUnknownCode", err)
	}

	expired, err := repo.CreateExpiredLinkCode(ctx, u.ID)
	if err != nil {
		t.Fatalf("CreateExpiredLinkCode: %v", err)
	}
	if _, err := repo.RedeemLinkCode(ctx, expired, "SHA256:ddd"); !errors.Is(err, store.ErrExpiredCode) {
		t.Errorf("expired redeem = %v, want ErrExpiredCode", err)
	}
}

func mustRecord(t *testing.T, repo *pg.Repo, userID string, wpm float64) {
	t.Helper()
	if err := repo.RecordRun(context.Background(), store.Run{
		UserID: userID, Mode: store.ModePractice, WPM: wpm, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
}

// closeEnough compares figures that make a round trip through float8 and an
// average, where exact equality is not the right test.
func closeEnough(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
