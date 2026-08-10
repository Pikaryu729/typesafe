package typing

import (
	"math"
	"testing"
	"time"
)

// fakeClock is a manually advanced time source.
type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

// typeString feeds every rune of s into the session.
func typeString(s *Session, text string) {
	for _, r := range text {
		s.Type(r)
	}
}

func assertClose(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestPerfectRun(t *testing.T) {
	s := New("hello world")
	typeString(s, "hello world")

	st := s.Stats()
	if !st.Finished {
		t.Error("session should be finished after typing the whole passage")
	}
	if st.Correct != 11 {
		t.Errorf("Correct = %d, want 11", st.Correct)
	}
	if st.Incorrect != 0 {
		t.Errorf("Incorrect = %d, want 0", st.Incorrect)
	}
	if st.Keystrokes != 11 {
		t.Errorf("Keystrokes = %d, want 11", st.Keystrokes)
	}
	assertClose(t, "Accuracy", st.Accuracy, 1)
	assertClose(t, "Progress", st.Progress, 1)
}

func TestRunWithErrors(t *testing.T) {
	s := New("cat")
	typeString(s, "cot")

	st := s.Stats()
	if st.Correct != 2 {
		t.Errorf("Correct = %d, want 2", st.Correct)
	}
	if st.Incorrect != 1 {
		t.Errorf("Incorrect = %d, want 1", st.Incorrect)
	}
	assertClose(t, "Accuracy", st.Accuracy, 2.0/3.0)

	if got := s.CharAt(1); got.State != Incorrect || got.Typed != 'o' || got.Target != 'a' {
		t.Errorf("CharAt(1) = %+v, want {Target:'a' Typed:'o' State:Incorrect}", got)
	}
}

func TestBackspaceRestoresUntypedState(t *testing.T) {
	s := New("cat")
	typeString(s, "co")
	s.Backspace()

	if s.Pos() != 1 {
		t.Errorf("Pos() = %d, want 1", s.Pos())
	}
	if got := s.CharAt(1); got.State != Untyped || got.Typed != 0 {
		t.Errorf("CharAt(1) = %+v, want an untyped character", got)
	}
	if st := s.Stats(); st.Incorrect != 0 {
		t.Errorf("Incorrect = %d after backspacing the error, want 0", st.Incorrect)
	}
}

// The point of counting keystrokes rather than final characters: correcting a
// mistake fixes the text but must not restore the accuracy you already lost.
func TestAccuracyIsNotRestoredByCorrections(t *testing.T) {
	s := New("cat")
	typeString(s, "co")
	s.Backspace()
	typeString(s, "at")

	st := s.Stats()
	if st.Correct != 3 || st.Incorrect != 0 {
		t.Errorf("final text = %d correct / %d incorrect, want 3/0", st.Correct, st.Incorrect)
	}
	if st.Keystrokes != 4 {
		t.Errorf("Keystrokes = %d, want 4 (backspace must not count)", st.Keystrokes)
	}
	assertClose(t, "Accuracy", st.Accuracy, 3.0/4.0)
}

func TestBackspaceAtStartIsNoOp(t *testing.T) {
	s := New("cat")
	s.Backspace()

	if s.Pos() != 0 {
		t.Errorf("Pos() = %d, want 0", s.Pos())
	}
	if s.Started() {
		t.Error("a backspace alone should not start the clock")
	}
}

func TestClockStartsOnFirstKeystroke(t *testing.T) {
	c := newFakeClock()
	s := New("hello", WithClock(c.now))

	c.add(30 * time.Second) // staring at the passage should not count
	if s.Started() {
		t.Error("session started before any keystroke")
	}
	if got := s.Elapsed(); got != 0 {
		t.Errorf("Elapsed() = %v before typing, want 0", got)
	}

	s.Type('h')
	c.add(2 * time.Second)

	if got := s.Elapsed(); got != 2*time.Second {
		t.Errorf("Elapsed() = %v, want 2s", got)
	}
}

func TestElapsedFreezesAtFinish(t *testing.T) {
	c := newFakeClock()
	s := New("ab", WithClock(c.now))

	s.Type('a')
	c.add(4 * time.Second)
	s.Type('b')
	c.add(10 * time.Minute) // sitting on the results screen

	if got := s.Elapsed(); got != 4*time.Second {
		t.Errorf("Elapsed() = %v after finishing, want 4s", got)
	}
}

func TestWPM(t *testing.T) {
	c := newFakeClock()
	// 25 correct characters is 5 "words"; over 30s that is 10 WPM.
	target := "abcdefghijabcdefghijabcde"
	s := New(target, WithClock(c.now))

	for i, r := range target {
		if i == 1 {
			c.add(30 * time.Second)
		}
		s.Type(r)
	}

	assertClose(t, "WPM", s.WPM(), 10)
	assertClose(t, "RawWPM", s.RawWPM(), 10)
}

func TestWPMExcludesIncorrectButRawDoesNot(t *testing.T) {
	c := newFakeClock()
	target := "abcdefghij" // 10 chars = 2 words
	s := New(target, WithClock(c.now))

	s.Type('a')
	c.add(60 * time.Second)
	// One correct so far, then nine wrong.
	for range 9 {
		s.Type('z')
	}

	assertClose(t, "WPM", s.WPM(), 1.0/charsPerWord)        // 1 correct char in 1 minute
	assertClose(t, "RawWPM", s.RawWPM(), 10.0/charsPerWord) // 10 keystrokes in 1 minute
}

func TestWPMIsZeroBeforeTimeAdvances(t *testing.T) {
	c := newFakeClock()
	s := New("hello", WithClock(c.now))
	s.Type('h')

	if got := s.WPM(); got != 0 {
		t.Errorf("WPM() = %v with no elapsed time, want 0", got)
	}
}

func TestTypingPastTheEndIsIgnored(t *testing.T) {
	s := New("ab")
	typeString(s, "abcdef")

	st := s.Stats()
	if st.Keystrokes != 2 {
		t.Errorf("Keystrokes = %d, want 2 (input after the end must be dropped)", st.Keystrokes)
	}
	if s.Pos() != 2 {
		t.Errorf("Pos() = %d, want 2", s.Pos())
	}
}

func TestBackspaceAfterFinishIsIgnored(t *testing.T) {
	s := New("ab")
	typeString(s, "ab")
	s.Backspace()

	if s.Pos() != 2 || !s.Finished() {
		t.Errorf("Pos() = %d, Finished() = %v; a finished session must stay finished", s.Pos(), s.Finished())
	}
}

func TestEmptyTarget(t *testing.T) {
	s := New("")

	if !s.Finished() {
		t.Error("an empty passage should be finished immediately")
	}
	assertClose(t, "Progress", s.Progress(), 1)
	if got := s.Elapsed(); got != 0 {
		t.Errorf("Elapsed() = %v, want 0", got)
	}
	s.Type('a') // must not panic
}

func TestProgress(t *testing.T) {
	s := New("abcd")
	assertClose(t, "Progress", s.Progress(), 0)

	s.Type('a')
	assertClose(t, "Progress", s.Progress(), 0.25)

	typeString(s, "xy")
	assertClose(t, "Progress", s.Progress(), 0.75) // wrong characters still advance
}

func TestAccuracyWithNoKeystrokes(t *testing.T) {
	if got := New("abc").Accuracy(); got != 1 {
		t.Errorf("Accuracy() = %v on an untouched session, want 1", got)
	}
}

func TestMultiByteRunes(t *testing.T) {
	s := New("héllo")
	typeString(s, "héllo")

	if s.Len() != 5 {
		t.Errorf("Len() = %d, want 5 runes", s.Len())
	}
	if st := s.Stats(); st.Correct != 5 || !st.Finished {
		t.Errorf("Stats() = %+v, want 5 correct and finished", st)
	}
}

func TestTargetRoundTrips(t *testing.T) {
	const want = "the quick brown fox"
	if got := New(want).Target(); got != want {
		t.Errorf("Target() = %q, want %q", got, want)
	}
}
