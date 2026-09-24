// Package lobby holds the state shared between connected users: which lobbies
// exist, who is in them, and how a race progresses.
//
// Everything here is safe for concurrent use. Each SSH session runs on its own
// goroutine and reaches into the same store, so this package is where the
// concurrency correctness of the app lives. It has no terminal or Bubble Tea
// dependency and can be tested without a PTY.
//
// # Locking
//
// Store and Lobby each have their own mutex and a strict rule: never hold both
// at once. Store.List copies lobby pointers, releases its lock, and only then
// asks each lobby for a snapshot; Lobby.Leave releases its own lock before
// asking the store to drop it. Without that discipline the two orderings would
// deadlock against each other.
//
// Broadcasting happens while a lobby's lock is held. That is deliberate and
// safe because every send is non-blocking, and it means subscribers observe
// events in a consistent order.
package lobby

import (
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Pikaryu729/typesafe/internal/cosmetics"
)

// MaxPlayers caps a lobby. Racing is rendered as one progress bar per player,
// so this is about what fits on a terminal as much as anything.
const MaxPlayers = 8

// subscriberBuffer is how many events a session can fall behind before it
// starts missing them. Progress updates are frequent but idempotent — a
// subscriber that drops one simply renders the next — so a modest buffer is
// enough to absorb a slow render without stalling the race.
const subscriberBuffer = 64

var (
	// ErrLobbyFull is returned when a lobby already holds MaxPlayers.
	ErrLobbyFull = errors.New("lobby is full")
	// ErrRaceStarted is returned when joining a lobby that is no longer waiting.
	ErrRaceStarted = errors.New("race already started")
	// ErrNotFound is returned for an unknown lobby code.
	ErrNotFound = errors.New("no such lobby")
	// ErrClosed is returned when acting on a lobby that has gone away.
	ErrClosed = errors.New("lobby closed")
	// ErrNotHost is returned when someone other than the host tries to start.
	ErrNotHost = errors.New("only the host can start the race")
	// ErrNotReady is returned when starting before everyone has readied up.
	ErrNotReady = errors.New("not everyone is ready")
	// ErrWrongPhase is returned for an action the lobby's phase does not allow.
	ErrWrongPhase = errors.New("lobby is not in the right phase")
)

// Phase is where a lobby is in the race cycle.
type Phase int

const (
	// PhaseWaiting means players are still joining and readying up.
	PhaseWaiting Phase = iota
	// PhaseCountdown means the race is counting in and the roster is locked.
	PhaseCountdown
	// PhaseRacing means the passage is live.
	PhaseRacing
	// PhaseFinished means the race is over and results stand.
	PhaseFinished
)

func (p Phase) String() string {
	switch p {
	case PhaseWaiting:
		return "waiting"
	case PhaseCountdown:
		return "starting"
	case PhaseRacing:
		return "racing"
	case PhaseFinished:
		return "finished"
	default:
		return "unknown"
	}
}

// PlayerState is one player as seen from outside the lobby. It is a value
// type: snapshots hand these out freely across goroutines.
type PlayerState struct {
	ID         string
	Name       string
	Flair      cosmetics.Flair
	Ready      bool
	CharsTyped int
	WPM        float64
	Accuracy   float64
	Place      int // finishing position, 0 until they finish
	Finished   bool
}

// Result is one player's final standing in a race.
type Result struct {
	PlayerID   string
	Name       string
	Flair      cosmetics.Flair
	Place      int // 0 if they did not finish
	WPM        float64
	Accuracy   float64
	CharsTyped int
	Elapsed    time.Duration
	Finished   bool // false if the race ended before they got there
}

// Snapshot is a consistent view of a lobby, safe to hand to another goroutine.
// Every slice in it is freshly allocated.
type Snapshot struct {
	Code    string
	HostID  string
	Phase   Phase
	Players []PlayerState // in join order
	Seed    int64
	Words   int
	StartAt time.Time
}

// Host returns the host's state, if the host is still present.
func (s Snapshot) Host() (PlayerState, bool) {
	for _, p := range s.Players {
		if p.ID == s.HostID {
			return p, true
		}
	}
	return PlayerState{}, false
}

// AllReady reports whether every player has readied up. A lobby holding only
// one player is not ready to race.
func (s Snapshot) AllReady() bool {
	if len(s.Players) < 2 {
		return false
	}
	for _, p := range s.Players {
		if !p.Ready {
			return false
		}
	}
	return true
}

// player is the store's own mutable record for a participant.
type player struct {
	id         string
	name       string
	flair      cosmetics.Flair
	ready      bool
	charsTyped int
	wpm        float64
	accuracy   float64
	place      int
	finishedAt time.Time
	finished   bool
}

func (p *player) state() PlayerState {
	return PlayerState{
		ID:         p.id,
		Name:       p.name,
		Flair:      p.flair,
		Ready:      p.ready,
		CharsTyped: p.charsTyped,
		WPM:        p.wpm,
		Accuracy:   p.accuracy,
		Place:      p.place,
		Finished:   p.finished,
	}
}

// Lobby is a group of players racing, or about to.
type Lobby struct {
	code  string
	store *Store

	mu      sync.RWMutex
	hostID  string
	players map[string]*player
	order   []string // join order: stable display, and who inherits the host
	phase   Phase
	seed    int64
	words   int
	startAt time.Time
	subs    map[string]chan Event
	closed  bool

	// finishers is the order players crossed the line, so places survive a
	// player leaving afterwards.
	finishers []string
	// abort stops the countdown or race-deadline goroutine early. It is
	// replaced on each phase change and closed to cancel the previous one.
	abort chan struct{}
}

// Code returns the lobby's join code.
func (l *Lobby) Code() string { return l.code }

// Snapshot returns a consistent view of the lobby.
func (l *Lobby) Snapshot() Snapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.snapshotLocked()
}

func (l *Lobby) snapshotLocked() Snapshot {
	players := make([]PlayerState, 0, len(l.order))
	for _, id := range l.order {
		if p, ok := l.players[id]; ok {
			players = append(players, p.state())
		}
	}
	return Snapshot{
		Code:    l.code,
		HostID:  l.hostID,
		Phase:   l.phase,
		Players: players,
		Seed:    l.seed,
		Words:   l.words,
		StartAt: l.startAt,
	}
}

// Join adds a player. The same ID joining twice is a no-op, which makes
// rejoining after a dropped screen harmless.
func (l *Lobby) Join(playerID, name string) error {
	l.mu.Lock()
	switch {
	case l.closed:
		l.mu.Unlock()
		return ErrClosed
	case l.players[playerID] != nil:
		l.mu.Unlock()
		return nil
	case l.phase != PhaseWaiting:
		l.mu.Unlock()
		return ErrRaceStarted
	case len(l.players) >= MaxPlayers:
		l.mu.Unlock()
		return ErrLobbyFull
	}

	l.players[playerID] = &player{id: playerID, name: name}
	l.order = append(l.order, playerID)
	l.broadcastLocked(LobbyUpdated{Snapshot: l.snapshotLocked()})
	l.mu.Unlock()
	return nil
}

// SetFlair records what this player's name and progress bar look like to
// everyone else in the lobby.
//
// A lobby carries a Flair the way it carries a Name, and interprets neither:
// both are opaque decoration that has to reach the other sessions somehow, and
// this is the only channel between them. Keeping it out of Join means the
// signature every caller already uses is unchanged, and it means a typist who
// equips something between races is seen wearing it rather than going stale.
//
// A player who is not in this lobby is silently ignored, the same as Ready.
func (l *Lobby) SetFlair(playerID string, f cosmetics.Flair) {
	l.mu.Lock()
	defer l.mu.Unlock()

	p := l.players[playerID]
	if l.closed || p == nil || p.flair == f {
		return
	}
	p.flair = f
	l.broadcastLocked(LobbyUpdated{Snapshot: l.snapshotLocked()})
}

// Leave removes a player, promoting a new host if that was the host and
// closing the lobby if nobody is left.
func (l *Lobby) Leave(playerID string) {
	l.mu.Lock()
	if l.closed || l.players[playerID] == nil {
		l.mu.Unlock()
		return
	}

	delete(l.players, playerID)
	l.order = removeString(l.order, playerID)
	l.closeSubLocked(playerID)

	// The host leaving hands the lobby to whoever joined next, rather than
	// stranding the remaining players in a lobby nobody can start.
	if l.hostID == playerID && len(l.order) > 0 {
		l.hostID = l.order[0]
	}

	empty := len(l.players) == 0
	if empty {
		l.closeLocked()
	} else {
		l.broadcastLocked(LobbyUpdated{Snapshot: l.snapshotLocked()})
		// Someone dropping out can be what ends a countdown or a race.
		l.reconcilePhaseLocked()
	}
	l.mu.Unlock()

	// Deliberately outside the lobby lock: taking the store's lock while
	// holding this one is the deadlock the package doc warns about.
	if empty {
		l.store.remove(l.code)
	}
}

// SetReady marks a player ready or not. It only applies while waiting.
func (l *Lobby) SetReady(playerID string, ready bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	p := l.players[playerID]
	if l.closed || p == nil || l.phase != PhaseWaiting || p.ready == ready {
		return
	}

	p.ready = ready
	l.broadcastLocked(LobbyUpdated{Snapshot: l.snapshotLocked()})
}

// Subscribe registers for the lobby's events. The returned channel is closed
// when the player leaves or the lobby closes, so a reader ranging over it
// terminates on its own.
//
// Subscribing twice replaces the previous channel, closing it first.
func (l *Lobby) Subscribe(playerID string) <-chan Event {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.closeSubLocked(playerID)

	ch := make(chan Event, subscriberBuffer)
	if l.closed {
		close(ch) // nothing will ever arrive; do not leave the caller waiting
		return ch
	}

	l.subs[playerID] = ch
	return ch
}

// Unsubscribe stops delivering events to a player without removing them.
func (l *Lobby) Unsubscribe(playerID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeSubLocked(playerID)
}

func (l *Lobby) closeSubLocked(playerID string) {
	if ch, ok := l.subs[playerID]; ok {
		delete(l.subs, playerID)
		close(ch)
	}
}

// broadcastLocked publishes to every subscriber without blocking.
//
// A session that has fallen a full buffer behind misses the event rather than
// holding up everyone else's race. That trade is safe because the events that
// matter most — phase changes — are also reflected in the next snapshot a
// subscriber receives.
func (l *Lobby) broadcastLocked(ev Event) {
	for _, ch := range l.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// closeLocked tears the lobby down, closing every remaining subscription. The
// caller must hold the write lock and is responsible for removing the lobby
// from the store afterwards.
func (l *Lobby) closeLocked() {
	if l.closed {
		return
	}
	l.closed = true
	l.closeAbortLocked() // stop any countdown or deadline goroutine
	for id := range l.subs {
		l.closeSubLocked(id)
	}
}

// Store is the registry of live lobbies. One exists per server process.
type Store struct {
	mu      sync.RWMutex
	lobbies map[string]*Lobby
	words   int
	rand    *rand.Rand

	now           func() time.Time
	countdownFrom int
	countdownTick time.Duration
	raceTimeout   time.Duration
}

// Option configures a Store.
type Option func(*Store)

// WithWords sets how many words a race passage holds.
func WithWords(n int) Option {
	return func(s *Store) {
		if n > 0 {
			s.words = n
		}
	}
}

// WithRand replaces the source used for join codes and passage seeds, so tests
// can make both deterministic.
func WithRand(r *rand.Rand) Option {
	return func(s *Store) { s.rand = r }
}

// WithClock replaces the time source used for race timing.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// WithCountdown sets how many ticks a race counts in for, and how long a tick
// lasts. Tests use a short tick to avoid waiting out a real countdown.
func WithCountdown(from int, tick time.Duration) Option {
	return func(s *Store) {
		if from > 0 {
			s.countdownFrom = from
		}
		if tick > 0 {
			s.countdownTick = tick
		}
	}
}

// WithRaceTimeout bounds how long a race can run before it is called.
func WithRaceTimeout(d time.Duration) Option {
	return func(s *Store) {
		if d > 0 {
			s.raceTimeout = d
		}
	}
}

// Defaults for a race.
const (
	// DefaultRaceWords is the passage length for a race. Shorter than solo
	// practice: a race that drags is a race people quit.
	DefaultRaceWords = 25
	// DefaultCountdown is how many seconds a race counts in for.
	DefaultCountdown = 3
	// DefaultRaceTimeout stops a race that nobody finishes from pinning the
	// lobby in the racing phase forever.
	DefaultRaceTimeout = 5 * time.Minute
)

// NewStore returns an empty registry.
func NewStore(opts ...Option) *Store {
	s := &Store{
		lobbies:       make(map[string]*Lobby),
		words:         DefaultRaceWords,
		rand:          rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
		now:           time.Now,
		countdownFrom: DefaultCountdown,
		countdownTick: time.Second,
		raceTimeout:   DefaultRaceTimeout,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Create opens a lobby with the given player as host, already joined.
func (s *Store) Create(hostID, hostName string) *Lobby {
	s.mu.Lock()
	defer s.mu.Unlock()

	l := &Lobby{
		code:    s.freeCodeLocked(),
		store:   s,
		hostID:  hostID,
		players: map[string]*player{hostID: {id: hostID, name: hostName}},
		order:   []string{hostID},
		phase:   PhaseWaiting,
		seed:    s.rand.Int64(),
		words:   s.words,
		subs:    make(map[string]chan Event),
	}
	s.lobbies[l.code] = l
	return l
}

// Get returns the lobby with the given code.
func (s *Store) Get(code string) (*Lobby, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	l, ok := s.lobbies[normalizeCode(code)]
	if !ok {
		return nil, ErrNotFound
	}
	return l, nil
}

// List returns a snapshot of every live lobby, oldest code order aside, sorted
// by code for a stable display.
func (s *Store) List() []Snapshot {
	// Copy the pointers, then release the store lock before snapshotting: the
	// package's locking rule is that these two mutexes are never held at once.
	s.mu.RLock()
	live := make([]*Lobby, 0, len(s.lobbies))
	for _, l := range s.lobbies {
		live = append(live, l)
	}
	s.mu.RUnlock()

	out := make([]Snapshot, 0, len(live))
	for _, l := range live {
		out = append(out, l.Snapshot())
	}
	sortSnapshots(out)
	return out
}

// Len reports how many lobbies are live.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.lobbies)
}

// remove drops a lobby from the registry. It is called by a lobby that has
// just closed, from outside that lobby's lock.
func (s *Store) remove(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lobbies, code)
}

// codeAlphabet omits characters that are easy to confuse when read aloud or
// off a screen: no O/0, I/1, S/5, Z/2.
const codeAlphabet = "ABCDEFGHJKLMNPQRTUVWXY346789"

// codeLength is short enough to type from memory; with this alphabet it still
// gives over half a million combinations.
const codeLength = 4

// freeCodeLocked picks a join code not already in use. The caller must hold
// the write lock.
func (s *Store) freeCodeLocked() string {
	for {
		code := make([]byte, codeLength)
		for i := range code {
			code[i] = codeAlphabet[s.rand.IntN(len(codeAlphabet))]
		}
		if _, taken := s.lobbies[string(code)]; !taken {
			return string(code)
		}
	}
}
