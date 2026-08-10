package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikary729/typesafe/internal/typing"
)

// practiceOver returns a Practice screen for a known passage, bypassing the
// random generator so tests can type an exact string.
func practiceOver(ctx *Context, target string) Practice {
	return Practice{ctx: ctx, sess: typing.New(target)}
}

// typeInto feeds text to a screen one rune at a time, as a terminal would.
func typeInto(s Screen, text string) (Screen, tea.Cmd) {
	var cmd tea.Cmd
	for _, r := range text {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		}
		s, cmd = s.Update(msg)
	}
	return s, cmd
}

func TestPracticeGeneratesAPassage(t *testing.T) {
	p := NewPractice(newTestContext())

	if p.sess.Len() == 0 {
		t.Fatal("practice started with an empty passage")
	}
	if got := len(strings.Fields(p.sess.Target())); got != practiceWords {
		t.Errorf("passage has %d words, want %d", got, practiceWords)
	}
}

func TestPracticeRecordsTyping(t *testing.T) {
	p := practiceOver(newTestContext(), "cat sat")
	s, _ := typeInto(p, "cat")

	st := s.(Practice).sess.Stats()
	if st.Correct != 3 {
		t.Errorf("Correct = %d, want 3", st.Correct)
	}
	if st.Keystrokes != 3 {
		t.Errorf("Keystrokes = %d, want 3", st.Keystrokes)
	}
}

func TestPracticeHandlesSpace(t *testing.T) {
	p := practiceOver(newTestContext(), "a b")
	s, _ := typeInto(p, "a b")

	if st := s.(Practice).sess.Stats(); st.Correct != 3 {
		t.Errorf("Correct = %d, want 3; the space key must reach the engine", st.Correct)
	}
}

func TestPracticeBackspace(t *testing.T) {
	p := practiceOver(newTestContext(), "cat")
	s, _ := typeInto(p, "co")
	s, _ = s.Update(tea.KeyMsg{Type: tea.KeyBackspace})

	if got := s.(Practice).sess.Pos(); got != 1 {
		t.Errorf("Pos = %d after backspace, want 1", got)
	}
}

func TestPracticeIgnoresAltCombinations(t *testing.T) {
	p := practiceOver(newTestContext(), "cat")
	s, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}, Alt: true})

	if got := s.(Practice).sess.Stats().Keystrokes; got != 0 {
		t.Errorf("Keystrokes = %d, want 0; alt-combinations are shortcuts", got)
	}
}

func TestPracticeFinishingNavigatesToResults(t *testing.T) {
	p := practiceOver(newTestContext(), "cat")
	_, cmd := typeInto(p, "cat")

	if cmd == nil {
		t.Fatal("completing the passage produced no command")
	}
	msg, ok := cmd().(navigateMsg)
	if !ok {
		t.Fatalf("completing the passage produced %T, want navigateMsg", cmd())
	}
	if _, ok := msg.to.(Results); !ok {
		t.Errorf("navigated to %T, want Results", msg.to)
	}
}

func TestPracticeResultsCarryTheAttemptsStats(t *testing.T) {
	p := practiceOver(newTestContext(), "cat")
	_, cmd := typeInto(p, "cot") // one mistake

	res := cmd().(navigateMsg).to.(Results)
	if res.stats.Correct != 2 || res.stats.Incorrect != 1 {
		t.Errorf("results carry %d correct / %d wrong, want 2/1",
			res.stats.Correct, res.stats.Incorrect)
	}
}

func TestPracticeEscapeReturnsToMenu(t *testing.T) {
	p := practiceOver(newTestContext(), "cat")
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if _, ok := cmd().(navigateMsg).to.(Menu); !ok {
		t.Error("esc did not return to the menu")
	}
}

func TestPracticeTabStartsADifferentPassage(t *testing.T) {
	p := practiceOver(newTestContext(), "cat")
	s, _ := typeInto(p, "ca")

	restarted, cmd := s.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd == nil {
		t.Error("restarting did not restart the stats clock")
	}

	next := restarted.(Practice)
	if next.sess.Pos() != 0 {
		t.Errorf("Pos = %d after restart, want 0", next.sess.Pos())
	}
	if next.sess.Target() == "cat" {
		t.Error("restart reused the same passage, want a new one")
	}
}

func TestPracticeTickStopsWhenFinished(t *testing.T) {
	p := practiceOver(newTestContext(), "ab")
	s, _ := typeInto(p, "ab")

	// After finishing, Practice is left behind for Results; its clock should
	// not keep rescheduling.
	_, cmd := s.Update(tickMsg(time.Now()))
	if cmd != nil {
		t.Error("the stats clock kept ticking after the attempt finished")
	}
}

func TestPracticeTickContinuesWhileTyping(t *testing.T) {
	p := practiceOver(newTestContext(), "cat")
	_, cmd := p.Update(tickMsg(time.Now()))

	if cmd == nil {
		t.Error("the stats clock stopped before the attempt finished")
	}
}

func TestPracticeViewShowsPassageAndStats(t *testing.T) {
	p := practiceOver(newTestContext(), "cat sat")
	view := plain(p.View())

	for _, want := range []string{"cat sat", "wpm", "acc", "time", "esc menu"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

func TestResultsRetryStartsNewPractice(t *testing.T) {
	res := newResults(newTestContext(), typing.Stats{})
	_, cmd := res.Update(key("r"))

	if _, ok := cmd().(navigateMsg).to.(Practice); !ok {
		t.Error("r did not start a new practice attempt")
	}
}

func TestResultsEscapeReturnsToMenu(t *testing.T) {
	res := newResults(newTestContext(), typing.Stats{})
	_, cmd := res.Update(key("esc"))

	if _, ok := cmd().(navigateMsg).to.(Menu); !ok {
		t.Error("esc did not return to the menu")
	}
}

func TestResultsViewShowsTheFigures(t *testing.T) {
	stats := typing.Stats{
		WPM: 82.4, RawWPM: 90.1, Accuracy: 0.955,
		Elapsed: 12300 * time.Millisecond, Correct: 100, Incorrect: 5, Keystrokes: 110,
	}
	view := plain(newResults(newTestContext(), stats).View())

	for _, want := range []string{"82", "96%", "12.3s", "100 correct", "5 wrong", "110 keystrokes", "90 raw wpm"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

func TestMenuPracticeItemOpensPractice(t *testing.T) {
	_, cmd := send(NewMenu(newTestContext()), "enter")

	if _, ok := cmd().(navigateMsg).to.(Practice); !ok {
		t.Error("the Practice menu item did not open the practice screen")
	}
}
