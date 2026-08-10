package lobby

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// raceStore returns a store whose countdown is fast enough to test against.
func raceStore(opts ...Option) *Store {
	return testStore(append([]Option{
		WithCountdown(2, time.Millisecond),
		WithRaceTimeout(2 * time.Second),
	}, opts...)...)
}

// readyLobby returns a lobby with n players, all readied up.
func readyLobby(t *testing.T, s *Store, n int) *Lobby {
	t.Helper()
	l := s.Create("host", "host")
	for i := 1; i < n; i++ {
		id := fmt.Sprintf("p%d", i)
		if err := l.Join(id, id); err != nil {
			t.Fatalf("Join %s: %v", id, err)
		}
	}
	for _, p := range l.Snapshot().Players {
		l.SetReady(p.ID, true)
	}
	return l
}

// collect drains ch into a slice until the lobby reaches phase, or it times out.
func collectUntil(t *testing.T, l *Lobby, ch <-chan Event, phase Phase) []Event {
	t.Helper()
	var events []Event
	deadline := time.After(3 * time.Second)
	for {
		if l.Snapshot().Phase == phase {
			return append(events, drain(ch)...)
		}
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("timed out waiting for phase %v (saw %d events)", phase, len(events))
		case <-time.After(time.Millisecond):
		}
	}
}

func eventsOfType[T Event](events []Event) []T {
	var out []T
	for _, ev := range events {
		if typed, ok := ev.(T); ok {
			out = append(out, typed)
		}
	}
	return out
}

func TestStartRequiresTheHost(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)

	if err := l.Start("p1"); err != ErrNotHost {
		t.Errorf("Start by a non-host = %v, want ErrNotHost", err)
	}
	if got := l.Snapshot().Phase; got != PhaseWaiting {
		t.Errorf("Phase = %v, want waiting", got)
	}
}

func TestStartRequiresEveryoneReady(t *testing.T) {
	s := raceStore()
	l := s.Create("host", "host")
	l.Join("p1", "one")
	l.SetReady("host", true)

	if err := l.Start("host"); err != ErrNotReady {
		t.Errorf("Start with someone unready = %v, want ErrNotReady", err)
	}
}

func TestStartRequiresTwoPlayers(t *testing.T) {
	s := raceStore()
	l := s.Create("host", "host")
	l.SetReady("host", true)

	if err := l.Start("host"); err != ErrNotReady {
		t.Errorf("Start alone = %v, want ErrNotReady", err)
	}
}

func TestCountdownReachesEverySubscriberOnce(t *testing.T) {
	s := raceStore(WithCountdown(3, time.Millisecond))
	l := readyLobby(t, s, 3)

	subs := map[string]<-chan Event{
		"host": l.Subscribe("host"),
		"p1":   l.Subscribe("p1"),
		"p2":   l.Subscribe("p2"),
	}
	if err := l.Start("host"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	for id, ch := range subs {
		events := collectUntil(t, l, ch, PhaseRacing)
		ticks := eventsOfType[CountdownTick](events)

		if len(ticks) != 3 {
			t.Errorf("%s saw %d countdown ticks, want 3", id, len(ticks))
		}
		for i, tick := range ticks {
			if want := 3 - i; tick.SecondsLeft != want {
				t.Errorf("%s tick %d counted %d, want %d", id, i, tick.SecondsLeft, want)
			}
		}
	}
}

// Every racer must measure from the same instant, or their WPM is not
// comparable and someone effectively starts early.
func TestEveryRacerGetsTheSameStartInstant(t *testing.T) {
	l := readyLobby(t, raceStore(), 3)

	subs := []<-chan Event{l.Subscribe("host"), l.Subscribe("p1"), l.Subscribe("p2")}
	if err := l.Start("host"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var starts []RaceStarted
	for _, ch := range subs {
		got := eventsOfType[RaceStarted](collectUntil(t, l, ch, PhaseRacing))
		if len(got) != 1 {
			t.Fatalf("subscriber saw %d RaceStarted events, want 1", len(got))
		}
		starts = append(starts, got[0])
	}

	for _, got := range starts[1:] {
		if !got.StartAt.Equal(starts[0].StartAt) {
			t.Errorf("StartAt differs between racers: %v vs %v", got.StartAt, starts[0].StartAt)
		}
		if got.Seed != starts[0].Seed {
			t.Errorf("Seed differs between racers: %d vs %d", got.Seed, starts[0].Seed)
		}
		if got.Words != starts[0].Words {
			t.Errorf("Words differs between racers: %d vs %d", got.Words, starts[0].Words)
		}
	}
	if starts[0].Seed != l.Snapshot().Seed {
		t.Error("the broadcast seed does not match the lobby's")
	}
}

func TestPhaseProgression(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)
	ch := l.Subscribe("host")

	if got := l.Snapshot().Phase; got != PhaseWaiting {
		t.Fatalf("Phase = %v before start, want waiting", got)
	}
	if err := l.Start("host"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := l.Snapshot().Phase; got != PhaseCountdown {
		t.Errorf("Phase = %v right after start, want countdown", got)
	}

	collectUntil(t, l, ch, PhaseRacing)
	if got := l.Snapshot().Phase; got != PhaseRacing {
		t.Errorf("Phase = %v, want racing", got)
	}

	l.Finish("host", 100, 80, 1)
	l.Finish("p1", 100, 70, 1)
	if got := l.Snapshot().Phase; got != PhaseFinished {
		t.Errorf("Phase = %v once everyone finished, want finished", got)
	}
}

func TestStartRejectedOutsideWaiting(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)
	if err := l.Start("host"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := l.Start("host"); err != ErrWrongPhase {
		t.Errorf("second Start = %v, want ErrWrongPhase", err)
	}
}

func TestFinishOrderIsStable(t *testing.T) {
	l := readyLobby(t, raceStore(), 3)
	ch := l.Subscribe("host")
	l.Start("host")
	collectUntil(t, l, ch, PhaseRacing)

	if got := l.Finish("p2", 100, 90, 1); got != 1 {
		t.Errorf("first finisher took place %d, want 1", got)
	}
	if got := l.Finish("host", 100, 80, 1); got != 2 {
		t.Errorf("second finisher took place %d, want 2", got)
	}
	if got := l.Finish("p1", 100, 70, 1); got != 3 {
		t.Errorf("third finisher took place %d, want 3", got)
	}

	results := l.Results()
	want := []string{"p2", "host", "p1"}
	for i, id := range want {
		if results[i].PlayerID != id || results[i].Place != i+1 {
			t.Errorf("place %d = %+v, want %s", i+1, results[i], id)
		}
	}
}

func TestFinishTwiceIsIgnored(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)
	ch := l.Subscribe("host")
	l.Start("host")
	collectUntil(t, l, ch, PhaseRacing)

	l.Finish("host", 100, 80, 1)
	if got := l.Finish("host", 100, 999, 1); got != 0 {
		t.Errorf("finishing twice returned place %d, want 0", got)
	}
	if got := l.Results()[0].WPM; got != 80 {
		t.Errorf("WPM = %v, want the first result to stand", got)
	}
}

func TestProgressIsRecordedAndBroadcast(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)
	ch := l.Subscribe("p1")
	l.Start("host")
	collectUntil(t, l, ch, PhaseRacing)

	l.RecordProgress("host", 42, 65.5, 0.97)

	events := drain(ch)
	prog := eventsOfType[ProgressUpdated](events)
	if len(prog) != 1 {
		t.Fatalf("saw %d progress events, want 1", len(prog))
	}
	if prog[0].PlayerID != "host" || prog[0].CharsTyped != 42 {
		t.Errorf("progress = %+v, want host at 42 characters", prog[0])
	}

	for _, p := range l.Snapshot().Players {
		if p.ID == "host" && p.CharsTyped != 42 {
			t.Errorf("snapshot has host at %d characters, want 42", p.CharsTyped)
		}
	}
}

func TestProgressIgnoredOutsideRacing(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)
	l.RecordProgress("host", 42, 65, 1)

	for _, p := range l.Snapshot().Players {
		if p.CharsTyped != 0 {
			t.Errorf("%s recorded progress while waiting", p.ID)
		}
	}
}

func TestRaceEndsWhenEveryoneFinishes(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)
	ch := l.Subscribe("host")
	l.Start("host")
	collectUntil(t, l, ch, PhaseRacing)

	l.Finish("host", 100, 80, 0.99)
	l.Finish("p1", 100, 70, 0.98)

	ended := eventsOfType[RaceEnded](drain(ch))
	if len(ended) != 1 {
		t.Fatalf("saw %d RaceEnded events, want 1", len(ended))
	}
	if len(ended[0].Results) != 2 {
		t.Errorf("standings hold %d entries, want 2", len(ended[0].Results))
	}
}

// A racer who walks away must not pin the lobby in the racing phase.
func TestRaceEndsWhenTheLastUnfinishedPlayerLeaves(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)
	ch := l.Subscribe("host")
	l.Start("host")
	collectUntil(t, l, ch, PhaseRacing)

	l.Finish("host", 100, 80, 1)
	if got := l.Snapshot().Phase; got != PhaseRacing {
		t.Fatalf("Phase = %v with one racer still going, want racing", got)
	}

	l.Leave("p1")

	if got := l.Snapshot().Phase; got != PhaseFinished {
		t.Errorf("Phase = %v after the last unfinished racer left, want finished", got)
	}
}

func TestRaceDeadlineCallsAStalledRace(t *testing.T) {
	s := raceStore(WithRaceTimeout(50 * time.Millisecond))
	l := readyLobby(t, s, 2)
	ch := l.Subscribe("host")
	l.Start("host")
	collectUntil(t, l, ch, PhaseRacing)

	waitFor(t, "the stalled race to be called", func() bool {
		return l.Snapshot().Phase == PhaseFinished
	})

	results := l.Results()
	if len(results) != 2 {
		t.Fatalf("standings hold %d entries, want 2", len(results))
	}
	for _, r := range results {
		if r.Finished {
			t.Errorf("%s is marked finished in a race nobody completed", r.Name)
		}
	}
}

func TestCountdownAbortedWhenTooFewPlayersRemain(t *testing.T) {
	s := raceStore(WithCountdown(20, 20*time.Millisecond))
	l := readyLobby(t, s, 2)
	l.Start("host")

	if got := l.Snapshot().Phase; got != PhaseCountdown {
		t.Fatalf("Phase = %v, want countdown", got)
	}

	l.Leave("p1")

	if got := l.Snapshot().Phase; got != PhaseWaiting {
		t.Errorf("Phase = %v after the lobby emptied out, want waiting", got)
	}

	// The countdown goroutine must not go on to start a race for one player.
	time.Sleep(100 * time.Millisecond)
	if got := l.Snapshot().Phase; got != PhaseWaiting {
		t.Errorf("Phase = %v later on, want the countdown abandoned", got)
	}
}

func TestJoinRejectedOnceRacing(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)
	ch := l.Subscribe("host")
	l.Start("host")
	collectUntil(t, l, ch, PhaseRacing)

	if err := l.Join("late", "latecomer"); err != ErrRaceStarted {
		t.Errorf("joining a running race = %v, want ErrRaceStarted", err)
	}
}

func TestUnfinishedPlayersRankBehindFinishersByProgress(t *testing.T) {
	s := raceStore(WithRaceTimeout(50 * time.Millisecond))
	l := readyLobby(t, s, 3)
	ch := l.Subscribe("host")
	l.Start("host")
	collectUntil(t, l, ch, PhaseRacing)

	l.Finish("p2", 100, 90, 1)
	l.RecordProgress("host", 10, 20, 1)
	l.RecordProgress("p1", 60, 40, 1)

	waitFor(t, "the race to be called", func() bool {
		return l.Snapshot().Phase == PhaseFinished
	})

	results := l.Results()
	want := []string{"p2", "p1", "host"} // finisher, then by distance covered
	for i, id := range want {
		if results[i].PlayerID != id {
			t.Errorf("standing %d = %s, want %s (%+v)", i, results[i].PlayerID, id, results)
			break
		}
	}
}

func TestRematchResetsForAnotherRace(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)
	ch := l.Subscribe("host")
	oldSeed := l.Snapshot().Seed
	l.Start("host")
	collectUntil(t, l, ch, PhaseRacing)
	l.Finish("host", 100, 80, 1)
	l.Finish("p1", 100, 70, 1)

	if err := l.Rematch("host"); err != nil {
		t.Fatalf("Rematch: %v", err)
	}

	snap := l.Snapshot()
	if snap.Phase != PhaseWaiting {
		t.Errorf("Phase = %v after a rematch, want waiting", snap.Phase)
	}
	if snap.Seed == oldSeed {
		t.Error("a rematch reused the same passage")
	}
	if len(snap.Players) != 2 {
		t.Errorf("rematch kept %d players, want 2", len(snap.Players))
	}
	for _, p := range snap.Players {
		if p.Ready || p.Finished || p.Place != 0 || p.CharsTyped != 0 || p.WPM != 0 {
			t.Errorf("%s carried state into the rematch: %+v", p.ID, p)
		}
	}
}

func TestRematchRequiresHostAndFinishedRace(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)

	if err := l.Rematch("host"); err != ErrWrongPhase {
		t.Errorf("Rematch while waiting = %v, want ErrWrongPhase", err)
	}

	ch := l.Subscribe("host")
	l.Start("host")
	collectUntil(t, l, ch, PhaseRacing)
	l.Finish("host", 100, 80, 1)
	l.Finish("p1", 100, 70, 1)

	if err := l.Rematch("p1"); err != ErrNotHost {
		t.Errorf("Rematch by a non-host = %v, want ErrNotHost", err)
	}
}

// The full lifecycle under concurrent pressure, which is how it will actually
// be driven: several sessions reporting progress while others read state.
func TestConcurrentRacing(t *testing.T) {
	s := raceStore()
	l := readyLobby(t, s, 4)

	ch := l.Subscribe("host")
	if err := l.Start("host"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	collectUntil(t, l, ch, PhaseRacing)

	ids := []string{"host", "p1", "p2", "p3"}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := range 200 {
				l.RecordProgress(id, i, float64(i), 0.99)
			}
			l.Finish(id, 200, 75, 0.99)
		}(id)
	}

	// Readers and subscribers churning throughout.
	for w := range 4 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			sub := l.Subscribe(fmt.Sprintf("watcher%d", w))
			for range 200 {
				_ = l.Snapshot()
				_ = l.Results()
				drain(sub)
			}
		}(w)
	}
	wg.Wait()

	waitFor(t, "the race to finish", func() bool { return l.Snapshot().Phase == PhaseFinished })

	results := l.Results()
	if len(results) != 4 {
		t.Fatalf("standings hold %d entries, want 4", len(results))
	}

	seen := make(map[int]bool)
	for _, r := range results {
		if !r.Finished {
			t.Errorf("%s did not finish", r.Name)
		}
		if seen[r.Place] {
			t.Errorf("place %d was awarded twice", r.Place)
		}
		seen[r.Place] = true
	}
	for place := 1; place <= 4; place++ {
		if !seen[place] {
			t.Errorf("nobody took place %d", place)
		}
	}
}
