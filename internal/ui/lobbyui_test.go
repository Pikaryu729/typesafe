package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikary729/typesafe/internal/lobby"
)

// twoContexts returns two sessions sharing one store, which is what a race
// actually looks like.
func twoContexts() (host, guest *Context) {
	store := lobby.NewStore(lobby.WithCountdown(1, time.Millisecond))

	mk := func(name, id string) *Context {
		return &Context{
			Username: name, PlayerID: id, Store: store,
			Styles: NewStyles(testRenderer()), Width: 80, Height: 24,
		}
	}
	return mk("alice", "alice-id"), mk("bob", "bob-id")
}

func TestBrowserStartsEmpty(t *testing.T) {
	b := NewBrowser(newTestContext())
	view := plain(b.View())

	if !strings.Contains(view, "no open lobbies") {
		t.Errorf("empty browser does not say so:\n%s", view)
	}
}

func TestBrowserCreateOpensWaitingRoom(t *testing.T) {
	ctx := newTestContext()
	_, cmd := NewBrowser(ctx).Update(key("c"))

	if cmd == nil {
		t.Fatal("c produced no command")
	}
	room, ok := cmd().(navigateMsg).to.(WaitingRoom)
	if !ok {
		t.Fatalf("c navigated to %T, want WaitingRoom", cmd().(navigateMsg).to)
	}
	if ctx.Store.Len() != 1 {
		t.Errorf("store holds %d lobbies, want 1", ctx.Store.Len())
	}
	if room.snap.HostID != ctx.PlayerID {
		t.Error("the creator is not the host")
	}
}

func TestBrowserListsAnotherSessionsLobby(t *testing.T) {
	host, guest := twoContexts()
	host.Store.Create(host.PlayerID, host.Username)

	b := NewBrowser(guest)
	view := plain(b.View())

	if len(b.lobbies) != 1 {
		t.Fatalf("browser sees %d lobbies, want 1", len(b.lobbies))
	}
	if !strings.Contains(view, "alice") {
		t.Errorf("lobby list does not name the host:\n%s", view)
	}
	if !strings.Contains(view, b.lobbies[0].Code) {
		t.Errorf("lobby list does not show the join code:\n%s", view)
	}
}

func TestBrowserRefreshPicksUpNewLobbies(t *testing.T) {
	host, guest := twoContexts()

	b := NewBrowser(guest)
	host.Store.Create(host.PlayerID, host.Username)

	updated, cmd := b.Update(refreshMsg(time.Now()))
	if cmd == nil {
		t.Error("refresh did not reschedule itself")
	}
	if got := len(updated.(Browser).lobbies); got != 1 {
		t.Errorf("browser sees %d lobbies after a refresh, want 1", got)
	}
}

func TestBrowserEnterJoinsSelectedLobby(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)

	_, cmd := NewBrowser(guest).Update(key("enter"))

	if _, ok := cmd().(navigateMsg).to.(WaitingRoom); !ok {
		t.Fatal("enter did not open the waiting room")
	}
	if got := len(l.Snapshot().Players); got != 2 {
		t.Errorf("lobby holds %d players after the join, want 2", got)
	}
}

func TestBrowserEnterOnAnEmptyListCreatesALobby(t *testing.T) {
	ctx := newTestContext()
	_, cmd := NewBrowser(ctx).Update(key("enter"))

	if _, ok := cmd().(navigateMsg).to.(WaitingRoom); !ok {
		t.Error("enter with nothing to join did not create a lobby")
	}
}

func TestBrowserJoinByCode(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)

	// "/" opens the prompt; the code is typed in lower case to check folding.
	s, _ := NewBrowser(guest).Update(key("/"))
	if !s.(Browser).typingCode {
		t.Fatal("/ did not open the join-code prompt")
	}
	s, _ = typeInto(s, strings.ToLower(l.Code()))
	if got := s.(Browser).code; got != l.Code() {
		t.Errorf("typed code = %q, want %q (input should fold to upper case)", got, l.Code())
	}

	_, cmd := s.Update(key("enter"))
	if _, ok := cmd().(navigateMsg).to.(WaitingRoom); !ok {
		t.Error("a valid code did not open the waiting room")
	}
	if got := len(l.Snapshot().Players); got != 2 {
		t.Errorf("lobby holds %d players, want 2", got)
	}
}

func TestBrowserJoinByCodeRejectsUnknownCode(t *testing.T) {
	ctx := newTestContext()
	s, _ := NewBrowser(ctx).Update(key("/"))
	s, _ = typeInto(s, "ZZZZ")
	s, cmd := s.Update(key("enter"))

	if cmd != nil {
		t.Error("an unknown code navigated somewhere")
	}
	if view := plain(s.View()); !strings.Contains(view, "no lobby with code ZZZZ") {
		t.Errorf("view does not explain the failure:\n%s", view)
	}
}

func TestBrowserCodePromptStopsAtCodeLength(t *testing.T) {
	s, _ := NewBrowser(newTestContext()).Update(key("/"))
	s, _ = typeInto(s, "ABCDEFGH")

	if got := s.(Browser).code; len(got) != codeLength {
		t.Errorf("code = %q, want it capped at %d characters", got, codeLength)
	}
}

func TestBrowserEscapeReturnsToMenu(t *testing.T) {
	_, cmd := NewBrowser(newTestContext()).Update(key("esc"))
	if _, ok := cmd().(navigateMsg).to.(Menu); !ok {
		t.Error("esc did not return to the menu")
	}
}

func TestBrowserCursorSurvivesALobbyDisappearing(t *testing.T) {
	host, guest := twoContexts()
	first := host.Store.Create("h1", "one")
	host.Store.Create("h2", "two")

	b := NewBrowser(guest)
	s, _ := b.Update(key("down")) // select the second entry
	if s.(Browser).cursor != 1 {
		t.Fatalf("cursor = %d, want 1", s.(Browser).cursor)
	}

	first.Leave("h1") // one lobby closes
	s, _ = s.Update(refreshMsg(time.Now()))

	got := s.(Browser)
	if got.cursor >= len(got.lobbies) {
		t.Errorf("cursor %d is out of range for %d lobbies", got.cursor, len(got.lobbies))
	}
}

func TestWaitingRoomShowsPlayersAndCode(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)
	l.Join(guest.PlayerID, guest.Username)

	w := NewWaitingRoom(host, l)
	w.snap = l.Snapshot()
	view := plain(w.View())

	for _, want := range []string{l.Code(), "alice", "bob", "(you)", "host"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

func TestWaitingRoomToggleReady(t *testing.T) {
	host, _ := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)

	room := NewWaitingRoom(host, l)
	room.Update(key("r"))
	if !l.Snapshot().Players[0].Ready {
		t.Fatal("r did not mark the player ready")
	}

	// Readiness is read back from the lobby, so the room needs the new
	// snapshot before it can toggle the other way.
	room.snap = l.Snapshot()
	room.Update(key("r"))
	if l.Snapshot().Players[0].Ready {
		t.Error("r a second time did not clear ready")
	}
}

func TestWaitingRoomStartRefusedForNonHost(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)
	l.Join(guest.PlayerID, guest.Username)
	l.SetReady(host.PlayerID, true)
	l.SetReady(guest.PlayerID, true)

	w := NewWaitingRoom(guest, l)
	w.snap = l.Snapshot()
	updated, _ := w.Update(key("enter"))

	if got := plain(updated.View()); !strings.Contains(got, "only the host") {
		t.Errorf("view does not explain who may start:\n%s", got)
	}
	if l.Snapshot().Phase != lobby.PhaseWaiting {
		t.Error("a non-host managed to start the race")
	}
}

func TestWaitingRoomStartRefusedUntilEveryoneIsReady(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)
	l.Join(guest.PlayerID, guest.Username)
	l.SetReady(host.PlayerID, true)

	w := NewWaitingRoom(host, l)
	w.snap = l.Snapshot()
	updated, _ := w.Update(key("enter"))

	if got := plain(updated.View()); !strings.Contains(got, "ready up") {
		t.Errorf("view does not say what is missing:\n%s", got)
	}
}

func TestWaitingRoomHostStartsTheCountdown(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)
	l.Join(guest.PlayerID, guest.Username)
	l.SetReady(host.PlayerID, true)
	l.SetReady(guest.PlayerID, true)

	w := NewWaitingRoom(host, l)
	w.snap = l.Snapshot()
	w.Update(key("enter"))

	if got := l.Snapshot().Phase; got != lobby.PhaseCountdown && got != lobby.PhaseRacing {
		t.Errorf("Phase = %v after the host started, want the race under way", got)
	}
}

func TestWaitingRoomRendersTheCountdown(t *testing.T) {
	host, _ := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)

	w, _ := NewWaitingRoom(host, l).Update(lobby.CountdownTick{SecondsLeft: 3})

	if got := plain(w.View()); !strings.Contains(got, "starting in 3") {
		t.Errorf("view does not show the countdown:\n%s", got)
	}
}

func TestWaitingRoomTracksLobbyUpdates(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)

	w := Screen(NewWaitingRoom(host, l))
	l.Join(guest.PlayerID, guest.Username)
	w, _ = w.Update(lobby.LobbyUpdated{Snapshot: l.Snapshot()})

	if got := len(w.(WaitingRoom).snap.Players); got != 2 {
		t.Errorf("room shows %d players after the update, want 2", got)
	}
}

func TestWaitingRoomEscapeLeavesTheLobby(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)
	l.Join(guest.PlayerID, guest.Username)

	w := NewWaitingRoom(guest, l)
	w.snap = l.Snapshot()
	_, cmd := w.Update(key("esc"))

	if _, ok := cmd().(navigateMsg).to.(Browser); !ok {
		t.Error("esc did not return to the browser")
	}
	if got := len(l.Snapshot().Players); got != 1 {
		t.Errorf("lobby still holds %d players, want 1", got)
	}
}

// The event pump is the bridge between shared state and the UI: a change made
// by another session has to arrive as a message in this one.
func TestSubscribeDeliversLobbyEventsToTheProgram(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)

	received := make(chan tea.Msg, 16)
	host.SetSender(func(msg tea.Msg) { received <- msg })
	host.enterLobby(l)

	// Another session joins; the host's program should hear about it.
	l.Join(guest.PlayerID, guest.Username)

	select {
	case msg := <-received:
		upd, ok := msg.(lobby.LobbyUpdated)
		if !ok {
			t.Fatalf("received %T, want lobby.LobbyUpdated", msg)
		}
		if len(upd.Snapshot.Players) != 2 {
			t.Errorf("event carries %d players, want 2", len(upd.Snapshot.Players))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event reached the program")
	}
}

func TestSubscribePumpStopsWhenTheLobbyCloses(t *testing.T) {
	host, _ := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)

	done := make(chan struct{})
	host.SetSender(func(tea.Msg) {})
	ch := l.Subscribe(host.PlayerID)
	go func() {
		defer close(done)
		for range ch {
		}
	}()

	l.Leave(host.PlayerID) // closes the lobby

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the pump goroutine outlived the lobby")
	}
}

func TestContextSendWithoutAProgramIsSafe(t *testing.T) {
	newTestContext().Send(lobby.CountdownTick{SecondsLeft: 1}) // must not panic
}

func TestMenuRaceItemOpensBrowser(t *testing.T) {
	_, cmd := send(NewMenu(newTestContext()), "j", "enter")

	if _, ok := cmd().(navigateMsg).to.(Browser); !ok {
		t.Error("the Race menu item did not open the lobby browser")
	}
}

// Disconnect is what stops a dropped client becoming a ghost: a player nobody
// can remove, in a lobby that therefore never closes.
func TestDisconnectLeavesTheLobby(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)
	host.enterLobby(l)
	l.Join(guest.PlayerID, guest.Username)
	guest.enterLobby(l)

	guest.Disconnect()

	if got := len(l.Snapshot().Players); got != 1 {
		t.Errorf("lobby holds %d players after a disconnect, want 1", got)
	}
	if guest.lobby != nil {
		t.Error("the session still thinks it is in a lobby")
	}
}

func TestDisconnectClosesAnAbandonedLobby(t *testing.T) {
	host, _ := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)
	host.enterLobby(l)

	host.Disconnect()

	if got := host.Store.Len(); got != 0 {
		t.Errorf("store holds %d lobbies after the last player dropped, want 0", got)
	}
}

func TestDisconnectOutsideALobbyIsSafe(t *testing.T) {
	newTestContext().Disconnect() // must not panic
}

func TestDisconnectIsIdempotent(t *testing.T) {
	host, _ := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)
	host.enterLobby(l)

	host.Disconnect()
	host.Disconnect() // a second teardown must not panic or double-leave
}

// Creating a lobby claims it immediately, so a client dropping between the
// create and the waiting room does not strand an empty lobby.
func TestCreatingALobbyClaimsItForCleanup(t *testing.T) {
	ctx := newTestContext()
	NewBrowser(ctx).Update(key("c"))

	if ctx.lobby == nil {
		t.Fatal("creating a lobby did not record it on the session")
	}
	ctx.Disconnect()
	if got := ctx.Store.Len(); got != 0 {
		t.Errorf("store holds %d lobbies, want the abandoned one closed", got)
	}
}

func TestJoiningALobbyClaimsItForCleanup(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)

	NewBrowser(guest).Update(key("enter"))

	if guest.lobby != l {
		t.Fatal("joining a lobby did not record it on the session")
	}
	guest.Disconnect()
	if got := len(l.Snapshot().Players); got != 1 {
		t.Errorf("lobby holds %d players, want the dropped joiner removed", got)
	}
}
