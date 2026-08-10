package lobby

import "time"

// Events published to a lobby's subscribers.
//
// These are plain structs on purpose. Bubble Tea's tea.Msg is an empty
// interface, so a session can hand one straight to Program.Send without this
// package importing Bubble Tea — the dependency runs one way, and the store
// stays testable without a terminal.

// Event is anything published to a lobby's subscribers.
type Event interface{ isEvent() }

// LobbyUpdated reports a change to the lobby's membership or readiness: a
// player joined or left, someone readied up, or the host changed. Subscribers
// re-read the lobby's snapshot rather than diffing.
type LobbyUpdated struct{ Snapshot Snapshot }

// CountdownTick counts a race in. It is broadcast once per second by the
// lobby, not timed independently by each session.
type CountdownTick struct{ SecondsLeft int }

// RaceStarted opens the race.
//
// StartAt is an absolute time rather than a duration so every racer measures
// elapsed time from the same instant, whatever their latency. It is also the
// clock the finishing order is judged against.
type RaceStarted struct {
	StartAt time.Time
	Seed    int64 // the passage seed, shared so nobody has to send the text
	Words   int   // how many words the passage holds
}

// ProgressUpdated reports one racer's position. It deliberately carries a
// count and a speed rather than the text, keeping the message small enough to
// send many times a second.
type ProgressUpdated struct {
	PlayerID   string
	CharsTyped int
	WPM        float64
}

// PlayerFinished reports a racer crossing the line, with the place they took.
type PlayerFinished struct {
	PlayerID string
	Place    int
	Result   Result
}

// RaceEnded reports the race over and carries the final standings.
type RaceEnded struct{ Results []Result }

// There is deliberately no "lobby closed" event. A lobby only closes once it
// is empty, so no subscriber could ever receive one. Closing a subscriber's
// channel is the signal instead: a reader ranging over it simply finishes.

func (LobbyUpdated) isEvent()    {}
func (CountdownTick) isEvent()   {}
func (RaceStarted) isEvent()     {}
func (ProgressUpdated) isEvent() {}
func (PlayerFinished) isEvent()  {}
func (RaceEnded) isEvent()       {}
