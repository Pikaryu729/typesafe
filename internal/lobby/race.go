package lobby

import (
	"slices"
	"time"
)

// The race lifecycle: waiting → countdown → racing → finished, and back to
// waiting on a rematch.
//
// Every transition is owned by the lobby, never by a session. A countdown is
// one goroutine broadcasting to everybody rather than each client timing its
// own, and the race opens with an absolute timestamp so all racers measure
// elapsed time from the same instant regardless of latency.

// Start begins the countdown. Only the host may start, and only once everyone
// has readied up.
func (l *Lobby) Start(playerID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch {
	case l.closed:
		return ErrClosed
	case l.phase != PhaseWaiting:
		return ErrWrongPhase
	case playerID != l.hostID:
		return ErrNotHost
	case !l.snapshotLocked().AllReady():
		return ErrNotReady
	}

	l.phase = PhaseCountdown
	abort := l.newAbortLocked()
	l.broadcastLocked(LobbyUpdated{Snapshot: l.snapshotLocked()})

	go l.runCountdown(abort)
	return nil
}

// runCountdown counts the race in and then opens it. It exits early if the
// countdown is aborted, which happens when the lobby drops below two players
// or empties out.
func (l *Lobby) runCountdown(abort chan struct{}) {
	for n := l.store.countdownFrom; n > 0; n-- {
		l.mu.Lock()
		if l.phase != PhaseCountdown {
			l.mu.Unlock()
			return
		}
		l.broadcastLocked(CountdownTick{SecondsLeft: n})
		l.mu.Unlock()

		select {
		case <-abort:
			return
		case <-time.After(l.store.countdownTick):
		}
	}
	l.beginRace(abort)
}

func (l *Lobby) beginRace(abort chan struct{}) {
	l.mu.Lock()
	if l.phase != PhaseCountdown {
		l.mu.Unlock()
		return
	}

	l.phase = PhaseRacing
	l.startAt = l.store.now()
	l.finishers = nil

	// Seed and length rather than the passage itself: every client generates
	// the same text from the same seed, so the message stays tiny.
	l.broadcastLocked(RaceStarted{StartAt: l.startAt, Seed: l.seed, Words: l.words})
	l.broadcastLocked(LobbyUpdated{Snapshot: l.snapshotLocked()})
	timeout := l.store.raceTimeout
	l.mu.Unlock()

	go l.watchRaceDeadline(abort, timeout)
}

// watchRaceDeadline calls a race that nobody finishes, so a lobby cannot be
// stranded in the racing phase by someone who walks away mid-passage.
func (l *Lobby) watchRaceDeadline(abort chan struct{}, timeout time.Duration) {
	select {
	case <-abort:
	case <-time.After(timeout):
		l.mu.Lock()
		if l.phase == PhaseRacing {
			l.endRaceLocked()
		}
		l.mu.Unlock()
	}
}

// RecordProgress updates one racer's position. Callers should throttle: this
// is cheap, but it fans out to every subscriber.
func (l *Lobby) RecordProgress(playerID string, charsTyped int, wpm, accuracy float64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	p := l.players[playerID]
	if l.closed || p == nil || l.phase != PhaseRacing || p.finished {
		return
	}

	p.charsTyped = charsTyped
	p.wpm = wpm
	p.accuracy = accuracy
	l.broadcastLocked(ProgressUpdated{PlayerID: playerID, CharsTyped: charsTyped, WPM: wpm})
}

// Finish records a racer crossing the line and returns the place they took.
//
// Elapsed time is measured server-side from the shared start instant, so
// places are decided by one clock rather than several.
func (l *Lobby) Finish(playerID string, charsTyped int, wpm, accuracy float64) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	p := l.players[playerID]
	if l.closed || p == nil || l.phase != PhaseRacing || p.finished {
		return 0
	}

	p.finished = true
	p.finishedAt = l.store.now()
	p.charsTyped = charsTyped
	p.wpm = wpm
	p.accuracy = accuracy

	l.finishers = append(l.finishers, playerID)
	p.place = len(l.finishers)

	l.broadcastLocked(PlayerFinished{
		PlayerID: playerID,
		Place:    p.place,
		Result:   l.resultLocked(p),
	})
	l.broadcastLocked(LobbyUpdated{Snapshot: l.snapshotLocked()})

	if l.everyoneFinishedLocked() {
		l.endRaceLocked()
	}
	return p.place
}

// reconcilePhaseLocked settles the phase after membership changes: a countdown
// with nobody left to race against is abandoned, and a race whose remaining
// players have all finished is called.
func (l *Lobby) reconcilePhaseLocked() {
	switch l.phase {
	case PhaseCountdown:
		if len(l.players) < 2 {
			l.phase = PhaseWaiting
			l.closeAbortLocked()
			l.broadcastLocked(LobbyUpdated{Snapshot: l.snapshotLocked()})
		}
	case PhaseRacing:
		if l.everyoneFinishedLocked() {
			l.endRaceLocked()
		}
	}
}

func (l *Lobby) everyoneFinishedLocked() bool {
	if len(l.players) == 0 {
		return false
	}
	for _, p := range l.players {
		if !p.finished {
			return false
		}
	}
	return true
}

// endRaceLocked closes the race and publishes the standings.
func (l *Lobby) endRaceLocked() {
	if l.phase == PhaseFinished {
		return
	}
	l.phase = PhaseFinished
	l.closeAbortLocked()

	results := l.resultsLocked()
	l.broadcastLocked(RaceEnded{Results: results})
	l.broadcastLocked(LobbyUpdated{Snapshot: l.snapshotLocked()})
}

// Results returns the standings of the most recent race.
func (l *Lobby) Results() []Result {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.resultsLocked()
}

// resultsLocked ranks finishers by the order they crossed, then everyone else
// by how far they got.
func (l *Lobby) resultsLocked() []Result {
	out := make([]Result, 0, len(l.players))
	for _, id := range l.order {
		if p, ok := l.players[id]; ok {
			out = append(out, l.resultLocked(p))
		}
	}

	slices.SortStableFunc(out, func(a, b Result) int {
		switch {
		case a.Finished && b.Finished:
			return a.Place - b.Place
		case a.Finished:
			return -1
		case b.Finished:
			return 1
		default:
			// Nobody who fell short has a place, so rank them by how far they
			// got. Ties keep join order, courtesy of the stable sort.
			return b.CharsTyped - a.CharsTyped
		}
	})
	return out
}

func (l *Lobby) resultLocked(p *player) Result {
	var elapsed time.Duration
	if p.finished && !l.startAt.IsZero() {
		elapsed = p.finishedAt.Sub(l.startAt)
	}
	return Result{
		PlayerID:   p.id,
		Name:       p.name,
		Place:      p.place,
		WPM:        p.wpm,
		Accuracy:   p.accuracy,
		CharsTyped: p.charsTyped,
		Elapsed:    elapsed,
		Finished:   p.finished,
	}
}

// Rematch returns a finished lobby to the waiting room with a fresh passage,
// keeping everyone who is still connected.
func (l *Lobby) Rematch(playerID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch {
	case l.closed:
		return ErrClosed
	case l.phase != PhaseFinished:
		return ErrWrongPhase
	case playerID != l.hostID:
		return ErrNotHost
	}

	l.phase = PhaseWaiting
	l.seed = l.store.rand.Int64()
	l.startAt = time.Time{}
	l.finishers = nil
	for _, p := range l.players {
		p.ready = false
		p.charsTyped = 0
		p.wpm = 0
		p.accuracy = 0
		p.place = 0
		p.finished = false
		p.finishedAt = time.Time{}
	}

	l.broadcastLocked(LobbyUpdated{Snapshot: l.snapshotLocked()})
	return nil
}

// newAbortLocked replaces the abort channel, cancelling whatever goroutine was
// watching the previous one.
func (l *Lobby) newAbortLocked() chan struct{} {
	l.closeAbortLocked()
	l.abort = make(chan struct{})
	return l.abort
}

func (l *Lobby) closeAbortLocked() {
	if l.abort != nil {
		close(l.abort)
		l.abort = nil
	}
}
