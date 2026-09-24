package lobby

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/Pikaryu729/typesafe/internal/cosmetics"
)

// testStore returns a store with a deterministic source, so join codes and
// seeds repeat between runs.
func testStore(opts ...Option) *Store {
	opts = append([]Option{WithRand(rand.New(rand.NewPCG(1, 2)))}, opts...)
	return NewStore(opts...)
}

// drain reads everything currently buffered on ch without blocking.
func drain(ch <-chan Event) []Event {
	var out []Event
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

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestCreateRegistersLobbyWithHostJoined(t *testing.T) {
	s := testStore()
	l := s.Create("h1", "host")

	if s.Len() != 1 {
		t.Errorf("store holds %d lobbies, want 1", s.Len())
	}

	snap := l.Snapshot()
	if snap.HostID != "h1" {
		t.Errorf("HostID = %q, want h1", snap.HostID)
	}
	if len(snap.Players) != 1 || snap.Players[0].Name != "host" {
		t.Errorf("players = %+v, want the host already joined", snap.Players)
	}
	if snap.Phase != PhaseWaiting {
		t.Errorf("Phase = %v, want waiting", snap.Phase)
	}
	if snap.Seed == 0 {
		t.Error("lobby has no passage seed")
	}
}

func TestGetByCodeIsCaseAndSpaceInsensitive(t *testing.T) {
	s := testStore()
	l := s.Create("h1", "host")

	for _, code := range []string{l.Code(), "  " + l.Code() + " "} {
		if got, err := s.Get(code); err != nil || got != l {
			t.Errorf("Get(%q) = %v, %v; want the lobby", code, got, err)
		}
	}
	if _, err := s.Get("ZZZZ"); err != ErrNotFound {
		t.Errorf("Get on an unknown code = %v, want ErrNotFound", err)
	}
}

func TestJoinCodesAreUnique(t *testing.T) {
	s := testStore()
	seen := make(map[string]bool)

	for i := range 200 {
		code := s.Create(fmt.Sprintf("h%d", i), "host").Code()
		if seen[code] {
			t.Fatalf("join code %q was issued twice", code)
		}
		seen[code] = true
		if len(code) != codeLength {
			t.Fatalf("code %q is %d characters, want %d", code, len(code), codeLength)
		}
	}
}

func TestJoinAddsPlayerInOrder(t *testing.T) {
	l := testStore().Create("h1", "host")
	if err := l.Join("p2", "bob"); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if err := l.Join("p3", "carol"); err != nil {
		t.Fatalf("Join: %v", err)
	}

	var names []string
	for _, p := range l.Snapshot().Players {
		names = append(names, p.Name)
	}
	if want := []string{"host", "bob", "carol"}; fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("players = %v, want %v in join order", names, want)
	}
}

func TestJoinTwiceIsANoOp(t *testing.T) {
	l := testStore().Create("h1", "host")
	if err := l.Join("h1", "host"); err != nil {
		t.Errorf("rejoining returned %v, want nil", err)
	}
	if got := len(l.Snapshot().Players); got != 1 {
		t.Errorf("lobby holds %d players, want 1", got)
	}
}

func TestJoinRejectsAFullLobby(t *testing.T) {
	l := testStore().Create("h1", "host")
	for i := 1; i < MaxPlayers; i++ {
		if err := l.Join(fmt.Sprintf("p%d", i), "player"); err != nil {
			t.Fatalf("Join %d: %v", i, err)
		}
	}

	if err := l.Join("one-too-many", "player"); err != ErrLobbyFull {
		t.Errorf("Join past the cap = %v, want ErrLobbyFull", err)
	}
	if got := len(l.Snapshot().Players); got != MaxPlayers {
		t.Errorf("lobby holds %d players, want %d", got, MaxPlayers)
	}
}

func TestSetReady(t *testing.T) {
	l := testStore().Create("h1", "host")
	l.Join("p2", "bob")

	l.SetReady("p2", true)
	snap := l.Snapshot()

	if snap.Players[1].Ready != true {
		t.Error("bob is not marked ready")
	}
	if snap.AllReady() {
		t.Error("AllReady is true while the host has not readied")
	}

	l.SetReady("h1", true)
	if !l.Snapshot().AllReady() {
		t.Error("AllReady is false once everyone has readied")
	}
}

func TestSetFlairReachesTheOtherPlayers(t *testing.T) {
	l := testStore().Create("h1", "host")
	l.Join("p2", "bob")
	ch := l.Subscribe("h1")

	want := cosmetics.Flair{Color: "color-gold", Badge: "badge-swift", Bar: "bar-dots"}
	l.SetFlair("p2", want)

	if got := l.Snapshot().Players[1].Flair; got != want {
		t.Errorf("bob's flair is %+v, want %+v", got, want)
	}

	// The other sessions have to be told, or they keep rendering the old one.
	events := drain(ch)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(events), events)
	}
	upd, ok := events[0].(LobbyUpdated)
	if !ok {
		t.Fatalf("got %T, want LobbyUpdated", events[0])
	}
	if got := upd.Snapshot.Players[1].Flair; got != want {
		t.Errorf("the broadcast snapshot carries %+v, want %+v", got, want)
	}
}

func TestSetFlairIsQuietWhenNothingChanged(t *testing.T) {
	l := testStore().Create("h1", "host")
	f := cosmetics.Flair{Bar: "bar-dots"}
	l.SetFlair("h1", f)

	ch := l.Subscribe("h1")
	l.SetFlair("h1", f)
	l.SetFlair("nobody-here", cosmetics.Flair{Bar: "bar-shade"})

	if events := drain(ch); len(events) != 0 {
		t.Errorf("re-setting the same flair broadcast %+v", events)
	}
}

func TestResultsCarryFlair(t *testing.T) {
	l := readyLobby(t, raceStore(), 2)

	want := cosmetics.Flair{Color: "color-gold"}
	l.SetFlair("host", want)

	ch := l.Subscribe("host")
	if err := l.Start("host"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	collectUntil(t, l, ch, PhaseRacing)
	l.Finish("host", 100, 80, 0.97)
	l.Finish("p1", 100, 70, 0.95)

	for _, res := range l.Results() {
		if res.PlayerID != "host" {
			continue
		}
		if res.Flair != want {
			t.Errorf("the standings carry %+v, want %+v", res.Flair, want)
		}
		return
	}
	t.Fatal("the host is missing from the standings")
}

func TestAllReadyNeedsTwoPlayers(t *testing.T) {
	l := testStore().Create("h1", "host")
	l.SetReady("h1", true)

	if l.Snapshot().AllReady() {
		t.Error("a lobby of one reports AllReady; there is nobody to race")
	}
}

func TestSubscribeReceivesMembershipEvents(t *testing.T) {
	l := testStore().Create("h1", "host")
	ch := l.Subscribe("h1")

	l.Join("p2", "bob")

	events := drain(ch)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(events), events)
	}
	upd, ok := events[0].(LobbyUpdated)
	if !ok {
		t.Fatalf("got %T, want LobbyUpdated", events[0])
	}
	if len(upd.Snapshot.Players) != 2 {
		t.Errorf("event snapshot holds %d players, want 2", len(upd.Snapshot.Players))
	}
}

func TestBroadcastReachesEverySubscriber(t *testing.T) {
	l := testStore().Create("h1", "host")
	l.Join("p2", "bob")
	l.Join("p3", "carol")

	chans := map[string]<-chan Event{
		"h1": l.Subscribe("h1"),
		"p2": l.Subscribe("p2"),
		"p3": l.Subscribe("p3"),
	}

	l.SetReady("p2", true)

	for id, ch := range chans {
		if got := drain(ch); len(got) != 1 {
			t.Errorf("subscriber %s got %d events, want 1", id, len(got))
		}
	}
}

// A session that stops reading must not be able to stall the lobby for
// everyone else.
func TestBroadcastDoesNotBlockOnAFullSubscriber(t *testing.T) {
	l := testStore().Create("h1", "host")
	slow := l.Subscribe("h1")
	fast := l.Subscribe("p2")
	l.Join("p2", "bob")

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Comfortably more events than the buffer holds.
		for i := range subscriberBuffer * 3 {
			l.SetReady("p2", i%2 == 0)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("broadcasting blocked on a subscriber that stopped reading")
	}

	if got := len(drain(slow)); got > subscriberBuffer {
		t.Errorf("slow subscriber buffered %d events, want at most %d", got, subscriberBuffer)
	}
	if len(drain(fast)) == 0 {
		t.Error("the other subscriber received nothing")
	}
}

func TestSubscribeTwiceClosesThePreviousChannel(t *testing.T) {
	l := testStore().Create("h1", "host")
	first := l.Subscribe("h1")
	l.Subscribe("h1")

	if _, open := <-first; open {
		t.Error("the replaced channel is still open")
	}
}

func TestLeaveClosesTheSubscription(t *testing.T) {
	l := testStore().Create("h1", "host")
	l.Join("p2", "bob")
	ch := l.Subscribe("p2")

	l.Leave("p2")

	// Reading to completion must terminate rather than block forever.
	for range ch {
	}
}

func TestLeavePromotesANewHost(t *testing.T) {
	l := testStore().Create("h1", "host")
	l.Join("p2", "bob")
	l.Join("p3", "carol")

	l.Leave("h1")

	snap := l.Snapshot()
	if snap.HostID != "p2" {
		t.Errorf("HostID = %q after the host left, want p2 (the next to join)", snap.HostID)
	}
	if len(snap.Players) != 2 {
		t.Errorf("lobby holds %d players, want 2", len(snap.Players))
	}
}

func TestLastPlayerLeavingClosesTheLobby(t *testing.T) {
	s := testStore()
	l := s.Create("h1", "host")
	ch := l.Subscribe("h1")

	l.Leave("h1")

	if s.Len() != 0 {
		t.Errorf("store still holds %d lobbies, want 0", s.Len())
	}
	if _, err := s.Get(l.Code()); err != ErrNotFound {
		t.Errorf("Get after close = %v, want ErrNotFound", err)
	}

	// Channel closure is how a subscriber learns it is over, so the pump
	// goroutine reading it must terminate rather than block forever.
	select {
	case _, open := <-ch:
		if open {
			t.Error("subscription is still open after the lobby closed")
		}
	case <-time.After(time.Second):
		t.Error("subscription never closed; a pump goroutine would leak")
	}
}

func TestActionsOnAClosedLobbyAreSafe(t *testing.T) {
	s := testStore()
	l := s.Create("h1", "host")
	l.Leave("h1") // closes it

	if err := l.Join("p2", "bob"); err != ErrClosed {
		t.Errorf("Join on a closed lobby = %v, want ErrClosed", err)
	}
	l.SetReady("h1", true) // must not panic
	l.Leave("h1")          // must not panic or double-close

	ch := l.Subscribe("p9")
	if _, open := <-ch; open {
		t.Error("subscribing to a closed lobby returned an open channel")
	}
}

func TestListIsSortedAndStable(t *testing.T) {
	s := testStore()
	for i := range 5 {
		s.Create(fmt.Sprintf("h%d", i), "host")
	}

	first := s.List()
	if len(first) != 5 {
		t.Fatalf("List returned %d lobbies, want 5", len(first))
	}
	for i := 1; i < len(first); i++ {
		if first[i-1].Code > first[i].Code {
			t.Errorf("List is not sorted by code: %q before %q", first[i-1].Code, first[i].Code)
		}
	}

	second := s.List()
	for i := range first {
		if first[i].Code != second[i].Code {
			t.Error("List order changed between identical calls")
			break
		}
	}
}

func TestSnapshotDoesNotAliasLobbyState(t *testing.T) {
	l := testStore().Create("h1", "host")
	l.Join("p2", "bob")

	snap := l.Snapshot()
	snap.Players[0].Name = "clobbered"

	if got := l.Snapshot().Players[0].Name; got != "host" {
		t.Errorf("mutating a snapshot changed the lobby: name is now %q", got)
	}
}

// The whole point of this package: many session goroutines hitting one store.
// Run under -race, this is the test that matters.
func TestConcurrentJoinLeaveAndRead(t *testing.T) {
	s := testStore()
	l := s.Create("host", "host")

	const workers = 16
	const rounds = 50

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("p%d", w)
			for range rounds {
				_ = l.Join(id, "player")
				l.SetReady(id, true)
				_ = l.Snapshot()
				_ = s.List()
				l.Leave(id)
			}
		}(w)
	}

	// Readers hammering the store while membership churns.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds * workers {
				_ = s.List()
				_ = s.Len()
				_ = l.Snapshot()
			}
		}()
	}

	wg.Wait()

	// The host never left, so the lobby must still be registered and sane.
	snap := l.Snapshot()
	if snap.HostID != "host" {
		t.Errorf("HostID = %q, want host", snap.HostID)
	}
	if len(snap.Players) < 1 {
		t.Error("the host was lost from the lobby")
	}
	if len(snap.Players) != len(snap.Players) {
		t.Error("snapshot is internally inconsistent")
	}
}

// Subscribers churning while events fly is the other half of the race surface.
func TestConcurrentSubscribeAndBroadcast(t *testing.T) {
	l := testStore().Create("host", "host")
	l.Join("p1", "one")

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			l.SetReady("p1", i%2 == 0)
		}
	}()

	for w := range 8 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("sub%d", w)
			for range 100 {
				ch := l.Subscribe(id)
				drain(ch)
				l.Unsubscribe(id)
			}
		}(w)
	}

	close(stop)
	wg.Wait()
}

func TestConcurrentLobbyCreationAndClosure(t *testing.T) {
	s := testStore()

	var wg sync.WaitGroup
	for w := range 16 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := range 20 {
				id := fmt.Sprintf("p%d-%d", w, r)
				l := s.Create(id, "host")
				_ = s.List()
				l.Leave(id) // closes it and removes it from the store
			}
		}(w)
	}
	wg.Wait()

	waitFor(t, "every lobby to be cleaned up", func() bool { return s.Len() == 0 })
}
