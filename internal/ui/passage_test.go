package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Pikaryu729/typesafe/internal/typing"
)

// ansi matches escape sequences so tests can assert on visible text. The
// Ascii-profile renderer drops colour, but attributes like underline survive.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// typedSession returns a session over target with input already applied.
func typedSession(target, input string) *typing.Session {
	s := typing.New(target)
	for _, r := range input {
		s.Type(r)
	}
	return s
}

func TestWrapLinesKeepsWordsWhole(t *testing.T) {
	target := []rune("the quick brown fox jumps over the lazy dog")

	for _, width := range []int{10, 15, 20, 40} {
		lines := wrapLines(target, width)
		for i, ln := range lines {
			text := string(target[ln.start:ln.end])
			if strings.TrimRight(text, " ") == "" {
				t.Errorf("width %d line %d is blank", width, i)
			}
			// Trailing spaces may run past the edge, and a single word longer
			// than the width is allowed to overflow. Anything else means a
			// break was missed.
			trimmed := strings.TrimRight(text, " ")
			if len(trimmed) > width && strings.Contains(trimmed, " ") {
				t.Errorf("width %d line %d is %d wide and could have wrapped: %q",
					width, i, len(trimmed), text)
			}
		}
		if joined := joinRanges(target, lines); joined != string(target) {
			t.Errorf("width %d lost text: %q", width, joined)
		}
	}
}

// joinRanges reassembles the ranges so wrapping can be checked for data loss.
func joinRanges(target []rune, lines []lineRange) string {
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(string(target[ln.start:ln.end]))
	}
	return b.String()
}

func TestWrapLinesSingleLineWhenItFits(t *testing.T) {
	lines := wrapLines([]rune("short text"), 80)
	if len(lines) != 1 {
		t.Errorf("got %d lines, want 1", len(lines))
	}
}

func TestWrapLinesEmptyTarget(t *testing.T) {
	if lines := wrapLines(nil, 40); len(lines) != 1 {
		t.Errorf("got %d lines for an empty target, want 1", len(lines))
	}
}

func TestWrapLinesBreaksAtExpectedPoints(t *testing.T) {
	target := []rune("aaa bbb ccc ddd")
	lines := wrapLines(target, 7)

	var got []string
	for _, ln := range lines {
		got = append(got, strings.TrimRight(string(target[ln.start:ln.end]), " "))
	}

	want := []string{"aaa bbb", "ccc ddd"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %v, want %v", got, want)
	}
}

func TestLineOf(t *testing.T) {
	target := []rune("aaa bbb ccc ddd")
	lines := wrapLines(target, 7) // "aaa bbb " / "ccc ddd"

	tests := []struct {
		pos  int
		want int
	}{
		{0, 0}, {7, 0}, {8, 1}, {14, 1},
		{len(target), 1}, // finished: cursor past the end
	}
	for _, tt := range tests {
		if got := lineOf(lines, tt.pos); got != tt.want {
			t.Errorf("lineOf(%d) = %d, want %d", tt.pos, got, tt.want)
		}
	}
}

func TestVisibleWindowShowsEverythingWhenItFits(t *testing.T) {
	lines := make([]lineRange, 3)
	if top, bottom := visibleWindow(lines, 0, 10); top != 0 || bottom != 3 {
		t.Errorf("window = [%d,%d), want [0,3)", top, bottom)
	}
}

func TestVisibleWindowKeepsCursorOnScreen(t *testing.T) {
	lines := make([]lineRange, 10)

	for cursor := range lines {
		top, bottom := visibleWindow(lines, cursor, 3)

		if bottom-top != 3 {
			t.Errorf("cursor %d: window height %d, want 3", cursor, bottom-top)
		}
		if cursor < top || cursor >= bottom {
			t.Errorf("cursor %d falls outside window [%d,%d)", cursor, top, bottom)
		}
		if top < 0 || bottom > len(lines) {
			t.Errorf("cursor %d: window [%d,%d) out of bounds", cursor, top, bottom)
		}
	}
}

func TestRenderPassageShowsWholeText(t *testing.T) {
	sess := typing.New("the quick brown fox")
	got := plain(renderPassage(newTestContext().Styles, sess, 80, 0))

	if got != "the quick brown fox" {
		t.Errorf("rendered %q, want the full passage", got)
	}
}

func TestRenderPassageWraps(t *testing.T) {
	sess := typing.New("aaa bbb ccc ddd")
	got := plain(renderPassage(newTestContext().Styles, sess, 7, 0))

	if want := "aaa bbb ccc ddd"; strings.ReplaceAll(got, "\n", "") != want {
		t.Errorf("rendered %q, want the passage split across lines", got)
	}
	if lines := strings.Count(got, "\n") + 1; lines != 2 {
		t.Errorf("rendered %d lines, want 2:\n%s", lines, got)
	}
}

func TestRenderPassageEmpty(t *testing.T) {
	if got := renderPassage(newTestContext().Styles, typing.New(""), 40, 0); got != "" {
		t.Errorf("rendered %q for an empty passage, want empty", got)
	}
}

// An error typed where a space belongs would be invisible if we drew the
// target character, so the typed one is shown instead.
func TestRenderPassageShowsTypedCharacterOverASpace(t *testing.T) {
	sess := typedSession("ab cd", "abx")
	got := plain(renderPassage(newTestContext().Styles, sess, 80, 0))

	if !strings.HasPrefix(got, "abx") {
		t.Errorf("rendered %q, want the wrong character shown in place of the space", got)
	}
}

// Everywhere else the target character is kept, so the passage stays readable.
func TestRenderPassageKeepsTargetCharacterOnError(t *testing.T) {
	sess := typedSession("cat", "cot")
	got := plain(renderPassage(newTestContext().Styles, sess, 80, 0))

	if got != "cat" {
		t.Errorf("rendered %q, want the target text %q", got, "cat")
	}
}

func TestRenderPassageScrollsWithTheCursor(t *testing.T) {
	target := strings.TrimSpace(strings.Repeat("word ", 40))
	sess := typing.New(target)

	// Type most of the passage so the cursor is near the end.
	for i, r := range []rune(target) {
		if i >= len([]rune(target))-10 {
			break
		}
		sess.Type(r)
	}

	got := renderPassage(newTestContext().Styles, sess, 20, 3)
	if lines := strings.Count(got, "\n") + 1; lines != 3 {
		t.Errorf("rendered %d lines, want 3", lines)
	}

	// The tail of the passage should be on screen, the very start should not.
	full := wrapLines([]rune(target), 20)
	if len(full) <= 3 {
		t.Fatal("test passage is too short to scroll")
	}
	firstLine := strings.TrimRight(string([]rune(target)[full[0].start:full[0].end]), " ")
	if strings.HasPrefix(plain(got), firstLine+"\n") {
		t.Error("window is still pinned to the top of a scrolled passage")
	}
}

func TestCellAtStates(t *testing.T) {
	sess := typedSession("cat", "co")

	tests := []struct {
		index int
		kind  cellKind
		r     rune
	}{
		{0, cellCorrect, 'c'},
		{1, cellIncorrect, 'a'}, // target kept, drawn as an error
		{2, cellCursor, 't'},
	}
	for _, tt := range tests {
		kind, r := cellAt(sess, tt.index)
		if kind != tt.kind || r != tt.r {
			t.Errorf("cellAt(%d) = (%v, %q), want (%v, %q)", tt.index, kind, r, tt.kind, tt.r)
		}
	}
}

func TestCellAtNoCursorWhenFinished(t *testing.T) {
	sess := typedSession("ab", "ab")

	for i := range 2 {
		if kind, _ := cellAt(sess, i); kind == cellCursor {
			t.Errorf("cellAt(%d) is a cursor on a finished session", i)
		}
	}
}
