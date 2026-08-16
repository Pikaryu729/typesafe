package ui

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Pikaryu729/typesafe/internal/cosmetics"
	"github.com/Pikaryu729/typesafe/internal/lobby"
	"github.com/Pikaryu729/typesafe/internal/store"
)

// trackedRacer returns a session with an in-memory repository behind it, as
// one of two contexts sharing a lobby store.
func trackedRacer(t *testing.T, ctx *Context, fingerprint string) *store.Memory {
	t.Helper()

	repo := store.NewMemory()
	user, err := repo.ResolveUser(context.Background(), fingerprint, ctx.Username)
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	ctx.Repo, ctx.User = repo, user
	return repo
}

// finishRace drives a race to its end and returns the standings screen.
func finishRace(t *testing.T, ctx *Context, l *lobby.Lobby, ev lobby.RaceStarted, results []lobby.Result) RaceResults {
	t.Helper()

	_, cmd := NewRace(ctx, l, ev).Update(lobby.RaceEnded{Results: results})
	if cmd == nil {
		t.Fatal("the end of the race produced no command")
	}
	nav, ok := cmd().(navigateMsg)
	if !ok {
		t.Fatalf("the end of the race produced %T, want navigateMsg", cmd())
	}
	res, ok := nav.to.(RaceResults)
	if !ok {
		t.Fatalf("navigated to %T, want RaceResults", nav.to)
	}
	return res
}

func TestRaceShowsAnEarningsBreakdown(t *testing.T) {
	host, guest := twoContexts()
	repo := trackedRacer(t, host, "SHA256:host")

	l, ev := racingLobby(t, host, guest)
	results := []lobby.Result{
		{PlayerID: host.PlayerID, Name: "alice", Place: 1, WPM: 82, Accuracy: 0.99, Finished: true},
		{PlayerID: guest.PlayerID, Name: "bob", Place: 2, WPM: 71, Accuracy: 0.93, Finished: true},
	}

	view := plain(finishRace(t, host, l, ev, results).View())

	for _, want := range []string{
		"you placed 1st",
		"base",
		"win bonus",
		"accuracy bonus",
		"speed bonus",
		"total",
		"balance",
		"bytes",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the breakdown is missing %q:\n%s", want, view)
		}
	}

	// The figure on screen has to be the figure that was banked, or the
	// breakdown is decoration.
	runs, err := repo.RecentRuns(context.Background(), host.User.ID, 10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Earned <= 0 {
		t.Fatalf("the stored run earned %d bytes", runs[0].Earned)
	}
	if host.Balance != runs[0].Earned {
		t.Errorf("the session balance is %d but the run banked %d", host.Balance, runs[0].Earned)
	}
	if !strings.Contains(view, strconv.Itoa(runs[0].Earned)) {
		t.Errorf("the total %d is not on screen:\n%s", runs[0].Earned, view)
	}
}

func TestBreakdownLinesAddUpToTheTotal(t *testing.T) {
	host, guest := twoContexts()
	trackedRacer(t, host, "SHA256:host")

	l, ev := racingLobby(t, host, guest)
	res := finishRace(t, host, l, ev, []lobby.Result{
		{PlayerID: host.PlayerID, Place: 1, WPM: 82, Accuracy: 0.99, Finished: true},
		{PlayerID: guest.PlayerID, Place: 2, WPM: 71, Accuracy: 0.93, Finished: true},
	})

	var sum int
	for _, l := range res.award.Lines {
		sum += l.Amount
	}
	if sum != res.award.Total {
		t.Errorf("the lines shown sum to %d but the total says %d", sum, res.award.Total)
	}
}

func TestNotFinishingEarnsNothingAndShowsNothing(t *testing.T) {
	host, guest := twoContexts()
	repo := trackedRacer(t, host, "SHA256:host")

	l, ev := racingLobby(t, host, guest)
	view := plain(finishRace(t, host, l, ev, []lobby.Result{
		{PlayerID: guest.PlayerID, Place: 1, WPM: 95, Accuracy: 0.99, Finished: true},
		{PlayerID: host.PlayerID, Finished: false},
	}).View())

	if strings.Contains(view, "win bonus") || strings.Contains(view, "total") {
		t.Errorf("a race nobody finished showed a breakdown:\n%s", view)
	}
	if host.Balance != 0 {
		t.Errorf("balance = %d after not finishing, want 0", host.Balance)
	}

	runs, _ := repo.RecentRuns(context.Background(), host.User.ID, 10)
	if len(runs) != 0 {
		t.Errorf("an unfinished race stored %+v", runs)
	}
}

func TestAnonymousSessionsEarnNothing(t *testing.T) {
	host, guest := twoContexts() // no repository, so nothing is tracked

	l, ev := racingLobby(t, host, guest)
	view := plain(finishRace(t, host, l, ev, []lobby.Result{
		{PlayerID: host.PlayerID, Place: 1, WPM: 82, Accuracy: 0.99, Finished: true},
		{PlayerID: guest.PlayerID, Place: 2, WPM: 71, Accuracy: 0.93, Finished: true},
	}).View())

	if strings.Contains(view, "bytes") {
		t.Errorf("an anonymous session was promised bytes it cannot keep:\n%s", view)
	}
	if host.Balance != 0 {
		t.Errorf("balance = %d for an anonymous session, want 0", host.Balance)
	}
}

func TestPracticeEarnsATrickleAndSaysSo(t *testing.T) {
	ctx, repo := trackedContext(t)

	p := NewPractice(ctx)
	typeOut(t, p, p.sess.Target())

	runs, err := repo.RecentRuns(context.Background(), ctx.User.ID, 10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Earned <= 0 {
		t.Errorf("practice earned %d bytes; it is meant to pay a trickle", runs[0].Earned)
	}
	if ctx.Balance != runs[0].Earned {
		t.Errorf("the session balance is %d but the run banked %d", ctx.Balance, runs[0].Earned)
	}
}

func TestMenuShowsTheBalance(t *testing.T) {
	ctx, _ := trackedContext(t)
	ctx.Balance = 287

	if view := plain(NewMenu(ctx).View()); !strings.Contains(view, "287 bytes") {
		t.Errorf("the menu does not show the balance:\n%s", view)
	}

	// An anonymous session has no balance to show, so it must not show a zero
	// as though it had one.
	if view := plain(NewMenu(newTestContext()).View()); strings.Contains(view, "bytes") {
		t.Errorf("an anonymous session was shown a balance:\n%s", view)
	}
}

func TestOpponentsFlairIsVisible(t *testing.T) {
	host, guest := twoContexts()

	// Bob has bought a title and a bar; alice is the one looking.
	guest.Equipped = cosmetics.Equipped{
		cosmetics.SlotBadge: "badge-swift",
		cosmetics.SlotBar:   "bar-dots",
	}

	l, ev := racingLobby(t, host, guest)
	l.SetFlair(guest.PlayerID, guest.flair())

	race := NewRace(host, l, ev)
	race.snap = l.Snapshot()
	view := plain(race.View())

	if !strings.Contains(view, "[swift]") {
		t.Errorf("alice cannot see bob's title:\n%s", view)
	}
	if !strings.Contains(view, "○") {
		t.Errorf("alice cannot see bob's bar glyphs:\n%s", view)
	}
	// Alice bought nothing, so her own bar is still the default.
	if !strings.Contains(view, cosmetics.DefaultEmpty) {
		t.Errorf("the default bar is gone:\n%s", view)
	}
}

func TestStandingsShowFlair(t *testing.T) {
	host, guest := twoContexts()
	trackedRacer(t, host, "SHA256:host")

	l, ev := racingLobby(t, host, guest)
	view := plain(finishRace(t, host, l, ev, []lobby.Result{
		{
			PlayerID: guest.PlayerID, Name: "bob", Place: 1, WPM: 95, Accuracy: 0.99,
			Finished: true, Flair: cosmetics.Flair{Badge: "badge-precise"},
		},
		{PlayerID: host.PlayerID, Name: "alice", Place: 2, WPM: 80, Accuracy: 0.95, Finished: true},
	}).View())

	if !strings.Contains(view, "[precise]") {
		t.Errorf("the standings do not show bob's title:\n%s", view)
	}
	if !strings.Contains(view, "alice") || !strings.Contains(view, "bob") {
		t.Errorf("a name went missing from the standings:\n%s", view)
	}
}
