package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Pikaryu729/typesafe/internal/lobby"
	"github.com/Pikaryu729/typesafe/internal/store"
	"github.com/Pikaryu729/typesafe/internal/words"
)

// typeOut types the whole passage correctly, which is how a test finishes a
// run without knowing what the passage says.
func typeOut(t *testing.T, s Screen, target string) Screen {
	t.Helper()

	for _, r := range target {
		s, _ = s.Update(key(string(r)))
	}
	return s
}

func TestPracticeRecordsAFinishedRun(t *testing.T) {
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

	got := runs[0]
	if got.Mode != store.ModePractice {
		t.Errorf("Mode = %q, want practice", got.Mode)
	}
	if got.WordCount != practiceWords {
		t.Errorf("WordCount = %d, want %d", got.WordCount, practiceWords)
	}
	// The seed is the whole point of storing a run this small: it has to
	// reproduce the passage that was actually typed.
	if want := words.Passage(got.Seed, got.WordCount); want != p.sess.Target() {
		t.Error("the stored seed does not reproduce the passage that was typed")
	}
	if got.Accuracy != 1 {
		t.Errorf("Accuracy = %v, want 1 for a clean run", got.Accuracy)
	}
	if got.Place != 0 || got.RaceCode != "" {
		t.Errorf("a practice run carries race data: place %d code %q", got.Place, got.RaceCode)
	}
}

func TestPracticeDoesNotRecordAnUnfinishedRun(t *testing.T) {
	ctx, repo := trackedContext(t)

	p := NewPractice(ctx)
	// A few characters and then a walk away.
	s, _ := p.Update(key(string([]rune(p.sess.Target())[0])))
	_ = s

	runs, _ := repo.RecentRuns(context.Background(), ctx.User.ID, 10)
	if len(runs) != 0 {
		t.Errorf("got %d runs, want none — the passage was not finished", len(runs))
	}
}

func TestPracticeWithoutAnAccountDoesNotPanic(t *testing.T) {
	ctx := newTestContext() // no Repo, no User

	p := NewPractice(ctx)
	typeOut(t, p, p.sess.Target())
	// Reaching here at all is the assertion: an untracked session must finish
	// a passage exactly as a tracked one does.
}

func TestRaceRecordsThisPlayersOwnResult(t *testing.T) {
	host, guest := twoContexts()

	// Give the host an account; the guest stays anonymous, which also checks
	// that one session recording does not depend on the others.
	repo := store.NewMemory()
	user, err := repo.ResolveUser(context.Background(), "SHA256:host", host.Username)
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	host.Repo, host.User = repo, user

	l, ev := racingLobby(t, host, guest)
	r := NewRace(host, l, ev)

	results := []lobby.Result{
		{PlayerID: guest.PlayerID, Place: 1, WPM: 95, Accuracy: 0.99, Finished: true},
		{PlayerID: host.PlayerID, Place: 2, WPM: 82, Accuracy: 0.94, Finished: true},
	}
	r.Update(lobby.RaceEnded{Results: results})

	runs, _ := repo.RecentRuns(context.Background(), user.ID, 10)
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want exactly this player's own", len(runs))
	}

	got := runs[0]
	if got.Mode != store.ModeRace {
		t.Errorf("Mode = %q, want race", got.Mode)
	}
	if got.Place != 2 {
		t.Errorf("Place = %d, want 2 — the wrong player's result was stored", got.Place)
	}
	// The lobby judges speed and accuracy against one clock, so its figures
	// are the ones that get stored.
	if got.WPM != 82 || got.Accuracy != 0.94 {
		t.Errorf("stored %v wpm at %v; want the lobby's 82 at 0.94", got.WPM, got.Accuracy)
	}
	if got.RaceCode != l.Code() {
		t.Errorf("RaceCode = %q, want %q", got.RaceCode, l.Code())
	}
	if want := words.Passage(got.Seed, got.WordCount); want != r.sess.Target() {
		t.Error("the stored seed does not reproduce the race passage")
	}
}

func TestRacePaysOnlyForFinishersInTheLobby(t *testing.T) {
	award := func(t *testing.T, guestFinished bool) int {
		t.Helper()
		host, guest := twoContexts()
		repo := store.NewMemory()
		user, err := repo.ResolveUser(context.Background(), "SHA256:host", host.Username)
		if err != nil {
			t.Fatalf("ResolveUser: %v", err)
		}
		host.Repo, host.User = repo, user
		l, ev := racingLobby(t, host, guest)
		r := NewRace(host, l, ev)
		_, cmd := r.Update(lobby.RaceEnded{Results: []lobby.Result{
			{PlayerID: host.PlayerID, Place: 1, WPM: 82, Accuracy: 0.94, Finished: true},
			{PlayerID: guest.PlayerID, Place: 2, WPM: 70, Accuracy: 0.90, Finished: guestFinished},
		}})
		return cmd().(navigateMsg).to.(RaceResults).award.Total
	}

	idle := award(t, false)
	duel := award(t, true)
	if idle >= duel {
		t.Errorf("winner with an idle lobby member earned %d, want less than a finished duel's %d", idle, duel)
	}
}

func TestRaceDoesNotRecordAPlayerWhoDidNotFinish(t *testing.T) {
	host, guest := twoContexts()

	repo := store.NewMemory()
	user, _ := repo.ResolveUser(context.Background(), "SHA256:host", host.Username)
	host.Repo, host.User = repo, user

	l, ev := racingLobby(t, host, guest)
	r := NewRace(host, l, ev)

	r.Update(lobby.RaceEnded{Results: []lobby.Result{
		{PlayerID: host.PlayerID, Place: 0, WPM: 40, Finished: false},
	}})

	runs, _ := repo.RecentRuns(context.Background(), user.ID, 10)
	if len(runs) != 0 {
		t.Errorf("got %d runs, want none — a half-typed passage is not a result", len(runs))
	}
}

func TestLinkIssuesACode(t *testing.T) {
	ctx, _ := trackedContext(t)

	s, cmd := send(NewLink(ctx), "c")
	if cmd == nil {
		t.Fatal("c produced no command")
	}
	s, _ = s.Update(cmd())

	got := plain(s.View())
	code := s.(Link).issued.Code
	if len(code) != store.LinkCodeLength {
		t.Fatalf("issued code %q, want %d characters", code, store.LinkCodeLength)
	}
	if !strings.Contains(got, code) {
		t.Errorf("the code is not shown:\n%s", got)
	}
	// It hands over the account, so the screen has to say so.
	if !strings.Contains(got, "anyone with it") {
		t.Errorf("view does not warn what the code grants:\n%s", got)
	}
}

func TestLinkRedeemingACodeSwitchesTheSessionsAccount(t *testing.T) {
	bg := context.Background()
	repo := store.NewMemory()

	laptop, _ := repo.ResolveUser(bg, "SHA256:laptop", "ryu")
	desktop, _ := repo.ResolveUser(bg, "SHA256:desktop", "ryu-desktop")

	// This session is the desktop, about to join the laptop's account.
	ctx := newTestContext()
	ctx.Repo, ctx.User, ctx.Fingerprint = repo, desktop, "SHA256:desktop"

	code, err := repo.CreateLinkCode(bg, laptop.ID)
	if err != nil {
		t.Fatalf("CreateLinkCode: %v", err)
	}

	s, _ := send(NewLink(ctx), "enter") // start the prompt
	s, cmd := send(s, append(strings.Split(code.Code, ""), "enter")...)
	if cmd == nil {
		t.Fatal("entering a code produced no command")
	}
	s, _ = s.Update(cmd())

	if ctx.User.ID != laptop.ID {
		t.Errorf("session account = %q, want the laptop's %q", ctx.User.ID, laptop.ID)
	}
	if ctx.Username != "ryu" {
		t.Errorf("display name = %q, want ryu", ctx.Username)
	}
	if got := plain(s.View()); !strings.Contains(got, "linked") {
		t.Errorf("view does not confirm the link:\n%s", got)
	}
}

func TestLinkFlushesQueuedRunsBeforeMergingAccounts(t *testing.T) {
	bg := context.Background()
	mem := store.NewMemory()
	target, _ := mem.ResolveUser(bg, "SHA256:target", "laptop")
	source, _ := mem.ResolveUser(bg, "SHA256:source", "desktop")
	code, err := mem.CreateLinkCode(bg, target.ID)
	if err != nil {
		t.Fatalf("CreateLinkCode: %v", err)
	}

	repo := store.NewAsync(slowRepo{Repository: mem, delay: 100 * time.Millisecond}, 4, nil)
	t.Cleanup(func() { _ = repo.Close() })
	ctx := newTestContext()
	ctx.Repo, ctx.User, ctx.Fingerprint = repo, source, "SHA256:source"
	if err := repo.RecordRun(bg, store.Run{UserID: source.ID, Mode: store.ModePractice, WPM: 70}); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	screen, _ := NewLink(ctx).Update(key("enter"))
	screen, _ = screen.Update(key(code.Code))
	screen, cmd := screen.Update(key("enter"))
	if cmd == nil {
		t.Fatal("redeeming a code produced no command")
	}
	screen, _ = screen.Update(cmd())
	if ctx.User.ID != target.ID {
		t.Errorf("session account = %q, want target %q", ctx.User.ID, target.ID)
	}

	runs, err := mem.RecentRuns(bg, target.ID, 10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("merged account has %d runs, want the queued source run", len(runs))
	}
}

func TestLinkReloadsTheMergedWallet(t *testing.T) {
	bg := context.Background()
	repo := store.NewMemory()
	target, _ := repo.ResolveUser(bg, "SHA256:target", "laptop")
	source, _ := repo.ResolveUser(bg, "SHA256:source", "desktop")
	if err := repo.RecordRun(bg, store.Run{UserID: target.ID, Earned: 50}); err != nil {
		t.Fatalf("target RecordRun: %v", err)
	}
	if err := repo.RecordRun(bg, store.Run{UserID: source.ID, Earned: 300}); err != nil {
		t.Fatalf("source RecordRun: %v", err)
	}
	if _, err := repo.Buy(bg, source.ID, store.Owned{ID: "bar-dots", Slot: "bar", Price: 120}); err != nil {
		t.Fatalf("Buy: %v", err)
	}
	if _, err := repo.Equip(bg, source.ID, "bar", "bar-dots"); err != nil {
		t.Fatalf("Equip: %v", err)
	}
	code, err := repo.CreateLinkCode(bg, target.ID)
	if err != nil {
		t.Fatalf("CreateLinkCode: %v", err)
	}

	ctx := newTestContext()
	ctx.Repo, ctx.User, ctx.Fingerprint = repo, source, "SHA256:source"
	wallet, _ := repo.Wallet(bg, source.ID)
	ctx.applyWallet(wallet)

	screen, _ := NewLink(ctx).Update(key("enter"))
	screen, _ = screen.Update(key(code.Code))
	screen, cmd := screen.Update(key("enter"))
	if cmd == nil {
		t.Fatal("redeeming a code produced no command")
	}
	screen, _ = screen.Update(cmd())
	if ctx.User.ID != target.ID {
		t.Errorf("session account = %q, want target %q", ctx.User.ID, target.ID)
	}
	if ctx.Balance != 230 {
		t.Errorf("merged balance = %d, want 230", ctx.Balance)
	}
	if got := ctx.flair().Bar; got != "" {
		t.Errorf("merged flair still wears %q, want the moved cosmetic unequipped", got)
	}
	if got := plain(screen.View()); !strings.Contains(got, "linked") {
		t.Errorf("link screen does not confirm the merge:\n%s", got)
	}
}

func TestLinkRejectsNonASCIICodeInput(t *testing.T) {
	ctx, _ := trackedContext(t)

	s, _ := send(NewLink(ctx), "enter", "é界é")
	link := s.(Link)
	if link.entry != "" {
		t.Errorf("non-ASCII input entered %q", link.entry)
	}
	if got := plain(link.View()); !strings.Contains(got, "______") {
		t.Errorf("link prompt width changed after non-ASCII input:\n%s", got)
	}
}

func TestLinkReportsABadCode(t *testing.T) {
	ctx, _ := trackedContext(t)

	s, _ := send(NewLink(ctx), "enter")
	s, cmd := send(s, append(strings.Split("ZZZZZZ", ""), "enter")...)
	s, _ = s.Update(cmd())

	if got := plain(s.View()); !strings.Contains(got, "no such code") {
		t.Errorf("view does not report an unknown code:\n%s", got)
	}
}

func TestLinkWithoutAnAccountOffersNothing(t *testing.T) {
	s, cmd := send(NewLink(newTestContext()), "c")
	if cmd != nil {
		t.Errorf("an untracked session issued a code: %T", cmd())
	}
	if got := plain(s.View()); !strings.Contains(got, "unavailable") {
		t.Errorf("view does not explain why linking is off:\n%s", got)
	}
}
