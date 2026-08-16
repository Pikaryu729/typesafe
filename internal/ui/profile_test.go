package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Pikaryu729/typesafe/internal/store"
)

// trackedContext returns a session context backed by an in-memory repository,
// as if the key had resolved to an account.
func trackedContext(t *testing.T) (*Context, *store.Memory) {
	t.Helper()

	repo := store.NewMemory()
	user, err := repo.ResolveUser(context.Background(), "SHA256:test", "tester")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}

	ctx := newTestContext()
	ctx.Repo = repo
	ctx.User = user
	ctx.Fingerprint = "SHA256:test"
	ctx.Username = user.DisplayName
	return ctx, repo
}

// loadScreen runs a screen's Init command and feeds the result back, which is how
// a screen that loads asynchronously reaches its loaded state in a test.
func loadScreen(t *testing.T, s Screen) Screen {
	t.Helper()

	cmd := s.Init()
	if cmd == nil {
		return s
	}
	next, _ := s.Update(cmd())
	return next
}

func TestProfileWithoutARepositoryExplainsItself(t *testing.T) {
	p := NewProfile(newTestContext()) // no Repo, no User
	got := plain(p.View())

	if !strings.Contains(got, "without a database") {
		t.Errorf("view does not say why there is no history:\n%s", got)
	}
}

func TestProfileWithARepositoryButNoAccountSaysAnonymous(t *testing.T) {
	ctx := newTestContext()
	ctx.Repo = store.NewMemory() // a database, but the lookup failed
	got := plain(NewProfile(ctx).View())

	if !strings.Contains(got, "anonymous") {
		t.Errorf("view does not distinguish an anonymous session:\n%s", got)
	}
}

func TestProfileWithNoRunsInvitesOne(t *testing.T) {
	ctx, _ := trackedContext(t)
	got := plain(loadScreen(t, NewProfile(ctx)).View())

	if !strings.Contains(got, "nothing here yet") {
		t.Errorf("empty profile does not invite a first run:\n%s", got)
	}
}

func TestProfileShowsBestsAndRuns(t *testing.T) {
	ctx, repo := trackedContext(t)
	bg := context.Background()

	for i, wpm := range []float64{60, 92, 71} {
		if err := repo.RecordRun(bg, store.Run{
			UserID: ctx.User.ID, Mode: store.ModePractice, WPM: wpm, Accuracy: 0.93,
			Duration: time.Minute, CreatedAt: time.Now().Add(-time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatalf("RecordRun: %v", err)
		}
	}
	if err := repo.RecordRun(bg, store.Run{
		UserID: ctx.User.ID, Mode: store.ModeRace, WPM: 80, Accuracy: 0.9,
		RaceCode: "QK4T", Place: 1, Duration: time.Minute, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	got := plain(loadScreen(t, NewProfile(ctx)).View())

	for _, want := range []string{"92", "best wpm", "4 runs", "1 races", "1 won", "race QK4T", "1st"} {
		if !strings.Contains(got, want) {
			t.Errorf("view is missing %q:\n%s", want, got)
		}
	}
}

func TestProfileReportsAFailedLoadWithoutAlarmingTheTypist(t *testing.T) {
	ctx, _ := trackedContext(t)
	ctx.Repo = failingRepo{Repository: ctx.Repo}

	got := plain(loadScreen(t, NewProfile(ctx)).View())

	if !strings.Contains(got, "could not load") {
		t.Errorf("a failed load is not reported:\n%s", got)
	}
	// The point being made to the user: this is not their problem.
	if !strings.Contains(got, "typing is unaffected") {
		t.Errorf("view does not reassure that typing still works:\n%s", got)
	}
}

func TestProfileEscapesToTheMenu(t *testing.T) {
	ctx, _ := trackedContext(t)
	_, cmd := send(NewProfile(ctx), "esc")

	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if _, ok := cmd().(navigateMsg); !ok {
		t.Errorf("esc produced %T, want navigateMsg", cmd())
	}
}

func TestSparkline(t *testing.T) {
	runs := func(wpms ...float64) []store.Run {
		out := make([]store.Run, 0, len(wpms))
		for _, w := range wpms {
			out = append(out, store.Run{WPM: w})
		}
		return out
	}

	t.Run("one run is not a trend", func(t *testing.T) {
		if got := sparkline(runs(80)); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("oldest is drawn first", func(t *testing.T) {
		// Runs arrive newest first, so this is a history that improved.
		got := []rune(sparkline(runs(90, 70, 50)))
		if len(got) != 3 {
			t.Fatalf("got %d columns, want 3", len(got))
		}
		if got[0] != '▁' || got[2] != '█' {
			t.Errorf("got %q, want a rising line", string(got))
		}
	})

	t.Run("a flat history does not divide by zero", func(t *testing.T) {
		got := sparkline(runs(70, 70, 70))
		if len([]rune(got)) != 3 {
			t.Fatalf("got %q, want three columns", got)
		}
		if strings.ContainsRune(got, '█') {
			t.Errorf("got %q; a flat history should sit mid-height", got)
		}
	})
}

func TestFormatAgo(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
	}
	for _, tt := range tests {
		if got := formatAgo(tt.in); got != tt.want {
			t.Errorf("formatAgo(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatTotalTime(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{20 * time.Minute, "20m"},
		{90 * time.Minute, "1h30m"},
	}
	for _, tt := range tests {
		if got := formatTotalTime(tt.in); got != tt.want {
			t.Errorf("formatTotalTime(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMenuDisablesAccountItemsWithoutAnAccount(t *testing.T) {
	got := plain(NewMenu(newTestContext()).View())
	if !strings.Contains(got, "no account on this server") {
		t.Errorf("account items are not marked unavailable:\n%s", got)
	}

	ctx, _ := trackedContext(t)
	got = plain(NewMenu(ctx).View())
	if strings.Contains(got, "no account on this server") {
		t.Errorf("account items are marked unavailable despite an account:\n%s", got)
	}
}

func TestMenuWillNotOpenADisabledItem(t *testing.T) {
	profile := -1
	for i, item := range menuItems {
		if item.title == "Profile" {
			profile = i
		}
	}
	if profile < 0 {
		t.Fatal("no Profile item on the menu")
	}

	m := NewMenu(newTestContext()) // untracked
	m.cursor = profile
	_, cmd := m.Update(key("enter"))

	if cmd != nil {
		t.Errorf("a disabled item produced a command: %T", cmd())
	}
}

// failingRepo makes every read fail, standing in for a database that has
// stopped answering.
type failingRepo struct {
	store.Repository
}

var errDown = errors.New("database is down")

func (failingRepo) Summary(context.Context, string) (store.Summary, error) {
	return store.Summary{}, errDown
}

func (failingRepo) RecentRuns(context.Context, string, int) ([]store.Run, error) {
	return nil, errDown
}

func (failingRepo) Wallet(context.Context, string) (store.Wallet, error) {
	return store.Wallet{}, errDown
}
