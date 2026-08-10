// Package typing implements the keystroke-level engine behind a typing
// attempt: it tracks what the user typed against a target passage, which
// characters are right or wrong, and the resulting speed and accuracy.
//
// The engine is deliberately free of any terminal or Bubble Tea dependency so
// it can be tested without a PTY. It is not safe for concurrent use; each
// session belongs to exactly one SSH session's goroutine.
package typing

import "time"

// CharState describes how the user has handled one character of the target.
type CharState int

const (
	// Untyped means the cursor has not reached this character yet, or the
	// user backspaced over it.
	Untyped CharState = iota
	// Correct means the user typed the expected character here.
	Correct
	// Incorrect means the user typed something else here.
	Incorrect
)

// Char is one position in the passage, as the renderer needs to see it.
type Char struct {
	Target rune      // the character the passage expects
	Typed  rune      // what the user entered, or 0 if Untyped
	State  CharState // whether Typed matches Target
}

// Stats is a snapshot of an attempt's performance.
type Stats struct {
	WPM        float64       // net words per minute, from correctly placed characters
	RawWPM     float64       // words per minute counting every keystroke, right or wrong
	Accuracy   float64       // correct keystrokes over total keystrokes, in [0,1]
	Progress   float64       // fraction of the passage the cursor has passed, in [0,1]
	Elapsed    time.Duration // time since the first keystroke
	Correct    int           // characters currently sitting correct
	Incorrect  int           // characters currently sitting wrong
	Keystrokes int           // character keystrokes entered, excluding backspaces
	Finished   bool          // whether the cursor has reached the end
}

// charsPerWord is the conventional divisor for words-per-minute: a "word" is
// five characters regardless of actual word boundaries.
const charsPerWord = 5

// Option configures a Session.
type Option func(*Session)

// WithClock replaces the time source, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Session) { s.now = now }
}

// Session is one attempt at typing a passage.
type Session struct {
	target []rune
	typed  []rune
	states []CharState
	pos    int

	keystrokes        int
	correctKeystrokes int
	correct           int
	incorrect         int

	now        func() time.Time
	startedAt  time.Time
	finishedAt time.Time
	started    bool
	finished   bool
}

// New starts a session over target. The clock does not begin until the first
// keystroke, so time spent looking at the passage is not counted.
//
// An empty target yields an already-finished session.
func New(target string, opts ...Option) *Session {
	runes := []rune(target)
	s := &Session{
		target:   runes,
		typed:    make([]rune, len(runes)),
		states:   make([]CharState, len(runes)),
		now:      time.Now,
		finished: len(runes) == 0,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Type records a single character keystroke. It is a no-op once the session
// has finished. Filtering out control keys is the caller's job.
func (s *Session) Type(r rune) {
	if s.finished {
		return
	}
	if !s.started {
		s.started = true
		s.startedAt = s.now()
	}

	s.typed[s.pos] = r
	s.keystrokes++
	if r == s.target[s.pos] {
		s.states[s.pos] = Correct
		s.correct++
		s.correctKeystrokes++
	} else {
		s.states[s.pos] = Incorrect
		s.incorrect++
	}
	s.pos++

	if s.pos == len(s.target) {
		s.finished = true
		s.finishedAt = s.now()
	}
}

// Backspace clears the character before the cursor and steps back.
//
// It deliberately does not count as a keystroke: accuracy is measured over the
// characters the user committed, so fixing a mistake never erases the mistake
// from the accuracy figure.
func (s *Session) Backspace() {
	if s.finished || s.pos == 0 {
		return
	}

	s.pos--
	switch s.states[s.pos] {
	case Correct:
		s.correct--
	case Incorrect:
		s.incorrect--
	}
	s.states[s.pos] = Untyped
	s.typed[s.pos] = 0
}

// Len reports the length of the passage in characters.
func (s *Session) Len() int { return len(s.target) }

// Pos reports the cursor's index into the passage.
func (s *Session) Pos() int { return s.pos }

// CharAt returns the state of position i. It panics if i is out of range,
// matching slice indexing.
func (s *Session) CharAt(i int) Char {
	return Char{Target: s.target[i], Typed: s.typed[i], State: s.states[i]}
}

// Target returns the passage being typed.
func (s *Session) Target() string { return string(s.target) }

// Started reports whether the first keystroke has landed.
func (s *Session) Started() bool { return s.started }

// Finished reports whether the cursor has reached the end of the passage. A
// session can finish with incorrect characters left in place.
func (s *Session) Finished() bool { return s.finished }

// Elapsed returns the time since the first keystroke, frozen at the moment the
// session finished. It is zero before typing begins.
func (s *Session) Elapsed() time.Duration {
	switch {
	case !s.started:
		return 0
	case s.finished:
		return s.finishedAt.Sub(s.startedAt)
	default:
		return s.now().Sub(s.startedAt)
	}
}

// Progress reports how far the cursor has moved through the passage, in [0,1].
func (s *Session) Progress() float64 {
	if len(s.target) == 0 {
		return 1
	}
	return float64(s.pos) / float64(len(s.target))
}

// Accuracy reports correct keystrokes over total keystrokes, in [0,1]. A
// session with no keystrokes yet reports 1.
func (s *Session) Accuracy() float64 {
	if s.keystrokes == 0 {
		return 1
	}
	return float64(s.correctKeystrokes) / float64(s.keystrokes)
}

// WPM reports net words per minute: correctly placed characters over elapsed
// time. It is zero until the clock has advanced.
func (s *Session) WPM() float64 { return s.wpm(s.correct) }

// RawWPM reports words per minute over every keystroke, ignoring correctness.
func (s *Session) RawWPM() float64 { return s.wpm(s.keystrokes) }

func (s *Session) wpm(chars int) float64 {
	elapsed := s.Elapsed()
	if elapsed <= 0 {
		return 0
	}
	return (float64(chars) / charsPerWord) / elapsed.Minutes()
}

// Stats returns a snapshot of the attempt.
func (s *Session) Stats() Stats {
	return Stats{
		WPM:        s.WPM(),
		RawWPM:     s.RawWPM(),
		Accuracy:   s.Accuracy(),
		Progress:   s.Progress(),
		Elapsed:    s.Elapsed(),
		Correct:    s.correct,
		Incorrect:  s.incorrect,
		Keystrokes: s.keystrokes,
		Finished:   s.finished,
	}
}
