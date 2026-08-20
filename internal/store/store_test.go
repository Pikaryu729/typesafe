package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Pikaryu729/typesafe/internal/store"
)

func run(userID string, wpm float64, opts ...func(*store.Run)) store.Run {
	r := store.Run{
		UserID:     userID,
		Mode:       store.ModePractice,
		Seed:       1,
		WordCount:  30,
		WPM:        wpm,
		Accuracy:   0.95,
		Duration:   30 * time.Second,
		Keystrokes: 150,
	}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

func asRace(place int) func(*store.Run) {
	return func(r *store.Run) {
		r.Mode = store.ModeRace
		r.RaceCode = "ABCD"
		r.Place = place
	}
}

func TestResolveUserCreatesOnceAndReusesTheKey(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()

	first, err := m.ResolveUser(ctx, "SHA256:aaa", "ryu")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if first.DisplayName != "ryu" {
		t.Errorf("display name = %q, want ryu", first.DisplayName)
	}

	// Same key, different name typed at the ssh prompt: identity follows the
	// key, so this must be the same account.
	again, err := m.ResolveUser(ctx, "SHA256:aaa", "someone-else")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if again.ID != first.ID {
		t.Errorf("second resolve made a new account: %q then %q", first.ID, again.ID)
	}

	// A different key is a different person until they link.
	other, err := m.ResolveUser(ctx, "SHA256:bbb", "ryu")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if other.ID == first.ID {
		t.Error("a different key resolved to the same account")
	}
}

func TestRecentRunsAreNewestFirstAndLimited(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()

	for i := range 5 {
		if err := m.RecordRun(ctx, run("u1", float64(60+i))); err != nil {
			t.Fatalf("RecordRun: %v", err)
		}
	}

	got, err := m.RecentRuns(ctx, "u1", 3)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d runs, want 3", len(got))
	}
	if got[0].WPM != 64 {
		t.Errorf("newest run has %v wpm, want 64 — order is wrong", got[0].WPM)
	}
}

func TestSummarize(t *testing.T) {
	runs := []store.Run{
		run("u1", 80, asRace(1)),
		run("u1", 70, asRace(3)),
		run("u1", 60),
	}
	runs[0].Accuracy = 0.9

	got := store.Summarize(runs)

	if got.Runs != 3 {
		t.Errorf("Runs = %d, want 3", got.Runs)
	}
	if got.Races != 2 {
		t.Errorf("Races = %d, want 2", got.Races)
	}
	if got.Wins != 1 {
		t.Errorf("Wins = %d, want 1", got.Wins)
	}
	if got.BestWPM != 80 {
		t.Errorf("BestWPM = %v, want 80", got.BestWPM)
	}
	// The accuracy reported is the one from the best run, not the best seen.
	if got.BestAccuracy != 0.9 {
		t.Errorf("BestAccuracy = %v, want 0.9", got.BestAccuracy)
	}
	if got.RecentWPM != 70 {
		t.Errorf("RecentWPM = %v, want 70", got.RecentWPM)
	}
	if got.TotalTime != 90*time.Second {
		t.Errorf("TotalTime = %v, want 1m30s", got.TotalTime)
	}
}

func TestSummarizeRecentWindowIgnoresOlderRuns(t *testing.T) {
	var runs []store.Run
	for range store.RecentWindow {
		runs = append(runs, run("u1", 100))
	}
	// Older than the window, and much slower: it must not drag the average.
	runs = append(runs, run("u1", 10))

	if got := store.Summarize(runs).RecentWPM; got != 100 {
		t.Errorf("RecentWPM = %v, want 100 — the window is not being applied", got)
	}
}

func TestSummaryOfAnUnknownUserIsEmpty(t *testing.T) {
	got, err := store.NewMemory().Summary(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got != (store.Summary{}) {
		t.Errorf("got %+v, want the zero Summary", got)
	}
}

func TestLinkCodeMergesTheTwoAccounts(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()

	laptop, _ := m.ResolveUser(ctx, "SHA256:laptop", "ryu")
	desktop, _ := m.ResolveUser(ctx, "SHA256:desktop", "ryu")
	if err := m.RecordRun(ctx, run(laptop.ID, 80)); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	if err := m.RecordRun(ctx, run(desktop.ID, 70)); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	code, err := m.CreateLinkCode(ctx, laptop.ID)
	if err != nil {
		t.Fatalf("CreateLinkCode: %v", err)
	}

	got, err := m.RedeemLinkCode(ctx, code.Code, "SHA256:desktop")
	if err != nil {
		t.Fatalf("RedeemLinkCode: %v", err)
	}
	if got.ID != laptop.ID {
		t.Errorf("redeemed into %q, want the issuing account %q", got.ID, laptop.ID)
	}

	// Both machines now reach one account...
	back, _ := m.ResolveUser(ctx, "SHA256:desktop", "ryu")
	if back.ID != laptop.ID {
		t.Errorf("desktop key still resolves to %q, want %q", back.ID, laptop.ID)
	}
	// ...and it holds the history of both.
	runs, _ := m.RecentRuns(ctx, laptop.ID, 10)
	if len(runs) != 2 {
		t.Fatalf("merged account has %d runs, want 2", len(runs))
	}
}

func TestLinkCodeIsSingleUse(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	u, _ := m.ResolveUser(ctx, "SHA256:aaa", "ryu")

	code, _ := m.CreateLinkCode(ctx, u.ID)
	if _, err := m.RedeemLinkCode(ctx, code.Code, "SHA256:bbb"); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if _, err := m.RedeemLinkCode(ctx, code.Code, "SHA256:ccc"); !errors.Is(err, store.ErrUnknownCode) {
		t.Errorf("second redeem error = %v, want ErrUnknownCode", err)
	}
}

func TestLinkCodeExpires(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	m := store.NewMemory(store.WithMemoryClock(func() time.Time { return now }))
	u, _ := m.ResolveUser(ctx, "SHA256:aaa", "ryu")

	code, _ := m.CreateLinkCode(ctx, u.ID)
	now = now.Add(store.LinkCodeTTL + time.Second)

	if _, err := m.RedeemLinkCode(ctx, code.Code, "SHA256:bbb"); !errors.Is(err, store.ErrExpiredCode) {
		t.Errorf("error = %v, want ErrExpiredCode", err)
	}
}

func TestRedeemUnknownCode(t *testing.T) {
	_, err := store.NewMemory().RedeemLinkCode(context.Background(), "ZZZZZZ", "SHA256:aaa")
	if !errors.Is(err, store.ErrUnknownCode) {
		t.Errorf("error = %v, want ErrUnknownCode", err)
	}
}

func TestAsyncWritesThrough(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()
	a := store.NewAsync(m, 8, func(error) { t.Error("unexpected error callback") })

	if err := a.RecordRun(ctx, run("u1", 80)); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	// Close drains, so no polling is needed to see the write land.
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	runs, _ := m.RecentRuns(ctx, "u1", 10)
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
}

func TestAsyncDropsRatherThanBlocks(t *testing.T) {
	ctx := context.Background()

	// A repository that never returns until released: the worker takes one run
	// and then wedges, so the buffer is all the capacity there is.
	release := make(chan struct{})
	blocked := &blockingRepo{Repository: store.NewMemory(), release: release}

	var mu sync.Mutex
	var dropped int
	a := store.NewAsync(blocked, 1, func(error) {
		mu.Lock()
		dropped++
		mu.Unlock()
	})

	// One goes to the worker, one fills the buffer, the rest have nowhere to be.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 10 {
			if err := a.RecordRun(ctx, run("u1", 80)); err != nil {
				t.Errorf("RecordRun: %v", err)
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RecordRun blocked on a full queue — it must drop instead")
	}

	mu.Lock()
	got := dropped
	mu.Unlock()
	if got == 0 {
		t.Error("nothing was reported as dropped")
	}

	close(release)
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAsyncRejectsWritesAfterClose(t *testing.T) {
	a := store.NewAsync(store.NewMemory(), 4, nil)
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := a.RecordRun(context.Background(), run("u1", 80)); !errors.Is(err, store.ErrClosed) {
		t.Errorf("error = %v, want ErrClosed", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// blockingRepo holds RecordRun until release is closed.
type blockingRepo struct {
	store.Repository
	release chan struct{}
	started chan struct{}
}

func (b *blockingRepo) RecordRun(ctx context.Context, r store.Run) error {
	if b.started != nil {
		close(b.started)
		b.started = nil
	}
	<-b.release
	return b.Repository.RecordRun(ctx, r)
}

func TestAsyncFlushWaitsForQueuedWrites(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	release := make(chan struct{})
	blocking := &blockingRepo{Repository: mem, release: release}

	a := store.NewAsync(blocking, 8, nil)
	t.Cleanup(func() { _ = a.Close() })

	if err := a.RecordRun(ctx, run("u1", 60)); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	// While the write is held, the underlying store genuinely has nothing —
	// which is the stale read a balance would otherwise be derived from.
	if runs, _ := mem.RecentRuns(ctx, "u1", 10); len(runs) != 0 {
		t.Fatal("the write landed before it was released; this test proves nothing")
	}

	flushed := make(chan error, 1)
	go func() { flushed <- store.Flush(ctx, a) }()

	select {
	case err := <-flushed:
		t.Fatalf("Flush returned while a write was still queued (err %v)", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-flushed:
		if err != nil {
			t.Fatalf("Flush: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Flush never returned after the write was released")
	}

	if runs, _ := mem.RecentRuns(ctx, "u1", 10); len(runs) != 1 {
		t.Errorf("got %d runs after Flush, want 1", len(runs))
	}
}

func TestAsyncFlushWaitsWhenItsQueueIsFull(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	release := make(chan struct{})
	started := make(chan struct{})
	blocking := &blockingRepo{Repository: mem, release: release, started: started}
	a := store.NewAsync(blocking, 1, nil)

	if err := a.RecordRun(ctx, run("u1", 60)); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	<-started
	if err := a.RecordRun(ctx, run("u1", 61)); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	flushed := make(chan error, 1)
	go func() { flushed <- store.Flush(ctx, a) }()
	select {
	case err := <-flushed:
		t.Fatalf("Flush returned while the full queue was pending: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-flushed:
		if err != nil {
			t.Fatalf("Flush: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Flush did not complete after the queue drained")
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if runs, _ := mem.RecentRuns(ctx, "u1", 10); len(runs) != 2 {
		t.Errorf("got %d runs after Flush, want 2", len(runs))
	}
}

func TestFlushIsANoOpForARepositoryThatDoesNotQueue(t *testing.T) {
	// Memory writes inline, so there is nothing to wait for and nothing to
	// fail. Callers must not have to know which kind of repository they hold.
	if err := store.Flush(context.Background(), store.NewMemory()); err != nil {
		t.Errorf("Flush on a synchronous repository: %v", err)
	}
	if err := store.Flush(context.Background(), nil); err != nil {
		t.Errorf("Flush on a nil repository: %v", err)
	}
}

func TestAsyncFlushAfterCloseDoesNotHang(t *testing.T) {
	a := store.NewAsync(store.NewMemory(), 4, nil)
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- store.Flush(context.Background(), a) }()

	select {
	case err := <-done:
		if !errors.Is(err, store.ErrClosed) {
			t.Errorf("Flush after Close = %v, want ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Flush hung on a closed repository")
	}
}
