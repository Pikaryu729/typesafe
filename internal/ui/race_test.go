package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/lobby"
	"github.com/Pikaryu729/typesafe/internal/words"
)

// racingLobby returns a lobby already in the racing phase, plus the start
// event a session would have received.
func racingLobby(t *testing.T, host, guest *Context) (*lobby.Lobby, lobby.RaceStarted) {
	t.Helper()

	l := host.Store.Create(host.PlayerID, host.Username)
	if err := l.Join(guest.PlayerID, guest.Username); err != nil {
		t.Fatalf("Join: %v", err)
	}
	l.SetReady(host.PlayerID, true)
	l.SetReady(guest.PlayerID, true)

	ch := l.Subscribe("observer")
	if err := l.Start(host.PlayerID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if started, ok := ev.(lobby.RaceStarted); ok {
				l.Unsubscribe("observer")
				return l, started
			}
		case <-deadline:
			t.Fatal("the race never started")
		}
	}
}

func TestRaceBuildsThePassageFromTheSharedSeed(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)

	a := NewRace(host, l, ev)
	b := NewRace(guest, l, ev)

	if a.sess.Target() != b.sess.Target() {
		t.Error("racers generated different passages from the same seed")
	}
	if want := words.Passage(ev.Seed, ev.Words); a.sess.Target() != want {
		t.Error("the passage does not match the seed the lobby broadcast")
	}
	if got := len(strings.Fields(a.sess.Target())); got != ev.Words {
		t.Errorf("passage has %d words, want %d", got, ev.Words)
	}
}

// Timing from the shared instant is what stops a slow starter getting a free
// head start, so it is worth asserting directly.
func TestRaceIsTimedFromTheSharedStart(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)
	ev.StartAt = time.Now().Add(-30 * time.Second) // as if we hesitated

	r := NewRace(host, l, ev)

	if !r.sess.Started() {
		t.Fatal("the race clock had not started before the first keystroke")
	}
	if got := r.sess.Elapsed(); got < 29*time.Second {
		t.Errorf("Elapsed = %v, want time counted from the shared start", got)
	}
}

func TestRaceTypingAdvancesTheSession(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)

	r := NewRace(host, l, ev)
	s, _ := typeInto(r, r.sess.Target()[:5])

	if got := s.(Race).sess.Pos(); got != 5 {
		t.Errorf("Pos = %d after five characters, want 5", got)
	}
}

func TestRaceReportsProgressOnlyWhenItMoves(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)

	ch := l.Subscribe("watcher")
	r := NewRace(host, l, ev)
	s, _ := typeInto(r, r.sess.Target()[:3])

	race := s.(Race)
	race.report()
	if got := len(eventsOfProgress(drain(ch))); got != 1 {
		t.Errorf("got %d progress events after typing, want 1", got)
	}

	// Nothing typed since, so nothing to say.
	race.report()
	if got := len(eventsOfProgress(drain(ch))); got != 0 {
		t.Errorf("got %d progress events while idle, want 0", got)
	}
}

func eventsOfProgress(events []lobby.Event) []lobby.ProgressUpdated {
	var out []lobby.ProgressUpdated
	for _, ev := range events {
		if p, ok := ev.(lobby.ProgressUpdated); ok {
			out = append(out, p)
		}
	}
	return out
}

func drain(ch <-chan lobby.Event) []lobby.Event {
	var out []lobby.Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestRaceFinishingRegistersWithTheLobby(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)

	r := NewRace(host, l, ev)
	s, _ := typeInto(r, r.sess.Target())

	if !s.(Race).finished {
		t.Fatal("the race screen did not register the finish")
	}

	var me lobby.PlayerState
	for _, p := range l.Snapshot().Players {
		if p.ID == host.PlayerID {
			me = p
		}
	}
	if !me.Finished || me.Place != 1 {
		t.Errorf("lobby has the finisher as %+v, want finished in first", me)
	}
}

func TestRaceIgnoresTypingAfterFinishing(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)

	r := NewRace(host, l, ev)
	s, _ := typeInto(r, r.sess.Target())
	before := s.(Race).sess.Stats().Keystrokes

	s, _ = typeInto(s, "xxxx")

	if got := s.(Race).sess.Stats().Keystrokes; got != before {
		t.Errorf("Keystrokes = %d after finishing, want %d", got, before)
	}
}

func TestRaceShowsOpponentProgress(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)

	r := Screen(NewRace(host, l, ev))
	r, _ = r.Update(lobby.ProgressUpdated{PlayerID: guest.PlayerID, CharsTyped: 40, WPM: 65})

	if got := r.(Race).progress[guest.PlayerID]; got.chars != 40 || got.wpm != 65 {
		t.Errorf("opponent progress = %+v, want 40 chars at 65 wpm", got)
	}

	view := plain(r.View())
	for _, want := range []string{"alice", "bob", "65 wpm"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

func TestRaceEndNavigatesToResults(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)

	results := []lobby.Result{{PlayerID: host.PlayerID, Name: "alice", Place: 1, Finished: true}}
	_, cmd := NewRace(host, l, ev).Update(lobby.RaceEnded{Results: results})

	if _, ok := cmd().(navigateMsg).to.(RaceResults); !ok {
		t.Error("the end of the race did not open the standings")
	}
}

func TestRaceEscapeLeavesTheLobby(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)

	_, cmd := NewRace(guest, l, ev).Update(tea.KeyMsg{Type: tea.KeyEsc})

	if _, ok := cmd().(navigateMsg).to.(Browser); !ok {
		t.Error("esc did not return to the browser")
	}
	for _, p := range l.Snapshot().Players {
		if p.ID == guest.PlayerID {
			t.Error("the player is still in the lobby after leaving")
		}
	}
}

func TestRenderBarClampsAndFills(t *testing.T) {
	tests := []struct {
		frac  float64
		want  int // filled cells
		label string
	}{
		{0, 0, "empty"},
		{0.5, barWidth / 2, "half"},
		{1, barWidth, "full"},
		{1.5, barWidth, "over-full is clamped"},
		{-1, 0, "negative is clamped"},
	}
	for _, tt := range tests {
		bar := renderBar(tt.frac)
		if got := len([]rune(bar)); got != barWidth {
			t.Errorf("%s: bar is %d cells wide, want %d", tt.label, got, barWidth)
		}
		if got := strings.Count(bar, "█"); got != tt.want {
			t.Errorf("%s: %d cells filled, want %d", tt.label, got, tt.want)
		}
	}
}

func TestOrdinal(t *testing.T) {
	tests := map[int]string{
		1: "1st", 2: "2nd", 3: "3rd", 4: "4th",
		11: "11th", 12: "12th", 13: "13th",
		21: "21st", 22: "22nd", 23: "23rd",
		0: "", -1: "",
	}
	for n, want := range tests {
		if got := ordinal(n); got != want {
			t.Errorf("ordinal(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRaceResultsShowStandings(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)

	results := []lobby.Result{
		{PlayerID: host.PlayerID, Name: "alice", Place: 1, WPM: 82, Accuracy: 0.97,
			Elapsed: 9500 * time.Millisecond, Finished: true},
		{PlayerID: guest.PlayerID, Name: "bob", Finished: false},
	}
	view := plain(NewRaceResults(host, l, results).View())

	for _, want := range []string{"1st", "alice", "82 wpm", "97%", "9.5s", "bob", "did not finish"} {
		if !strings.Contains(view, want) {
			t.Errorf("standings are missing %q:\n%s", want, view)
		}
	}
}

func TestRaceResultsRematchIsHostOnly(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)

	// Both racers finish, which ends the race.
	for _, ctx := range []*Context{guest, host} {
		r := NewRace(ctx, l, ev)
		typeInto(r, r.sess.Target())
	}

	res := NewRaceResults(guest, l, l.Results())
	updated, _ := res.Update(key("enter"))

	if got := plain(updated.View()); !strings.Contains(got, "only the host") {
		t.Errorf("view does not explain who may call a rematch:\n%s", got)
	}
}

func TestRaceResultsHostRematchReturnsEveryoneToTheRoom(t *testing.T) {
	host, guest := twoContexts()
	l, ev := racingLobby(t, host, guest)

	for _, ctx := range []*Context{host, guest} {
		r := NewRace(ctx, l, ev)
		typeInto(r, r.sess.Target())
	}
	if got := l.Snapshot().Phase; got != lobby.PhaseFinished {
		t.Fatalf("Phase = %v after both finished, want finished", got)
	}

	// The host calls it...
	res := NewRaceResults(host, l, l.Results())
	res.Update(key("enter"))
	if got := l.Snapshot().Phase; got != lobby.PhaseWaiting {
		t.Fatalf("Phase = %v after a rematch, want waiting", got)
	}

	// ...and the resulting update is what moves the other player.
	guestRes := Screen(NewRaceResults(guest, l, l.Results()))
	_, cmd := guestRes.Update(lobby.LobbyUpdated{Snapshot: l.Snapshot()})

	if _, ok := cmd().(navigateMsg).to.(WaitingRoom); !ok {
		t.Error("the other player was not returned to the waiting room")
	}
}

func TestRaceResultsEscapeLeavesTheLobby(t *testing.T) {
	host, guest := twoContexts()
	l := host.Store.Create(host.PlayerID, host.Username)
	l.Join(guest.PlayerID, guest.Username)

	_, cmd := NewRaceResults(guest, l, nil).Update(key("esc"))

	if _, ok := cmd().(navigateMsg).to.(Browser); !ok {
		t.Error("esc did not return to the browser")
	}
	if got := len(l.Snapshot().Players); got != 1 {
		t.Errorf("lobby holds %d players after leaving, want 1", got)
	}
}
