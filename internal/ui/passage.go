package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Pikaryu729/typesafe/internal/typing"
)

// Rendering of the passage being typed, with per-character feedback. Shared by
// solo practice and races so both look and wrap identically.

// cellKind is how one character should be drawn. It exists so that runs of
// identically styled characters can be emitted in a single Render call rather
// than one escape sequence per character.
type cellKind int

const (
	cellUntyped cellKind = iota
	cellCorrect
	cellIncorrect
	cellCursor
)

func (k cellKind) style(s Styles) lipgloss.Style {
	switch k {
	case cellCorrect:
		return s.Correct
	case cellIncorrect:
		return s.Incorrect
	case cellCursor:
		return s.Cursor
	default:
		return s.Untyped
	}
}

// lineRange is a half-open [start, end) range of rune indices into the target.
type lineRange struct{ start, end int }

// wrapLines splits the passage into display lines on word boundaries.
//
// A word longer than width is left to overflow rather than being split, which
// keeps the mapping from rune index to screen position simple. The generated
// word list tops out around eleven characters, so this cannot bite in practice.
func wrapLines(target []rune, width int) []lineRange {
	if width < 1 {
		width = 1
	}

	var (
		lines   []lineRange
		start   int
		lineLen int
		i       int
	)
	for i < len(target) {
		wordStart := i
		for i < len(target) && target[i] != ' ' {
			i++
		}
		wordLen := i - wordStart

		// Trailing spaces stay on the line they follow, so the cursor has
		// somewhere to sit while the user types the separator.
		for i < len(target) && target[i] == ' ' {
			i++
		}

		if lineLen > 0 && lineLen+wordLen > width {
			lines = append(lines, lineRange{start, wordStart})
			start = wordStart
			lineLen = 0
		}
		lineLen += i - wordStart
	}

	if start < len(target) || len(lines) == 0 {
		lines = append(lines, lineRange{start, len(target)})
	}
	return lines
}

// lineOf reports which display line holds rune index pos.
func lineOf(lines []lineRange, pos int) int {
	for i, ln := range lines {
		if pos < ln.end {
			return i
		}
	}
	return len(lines) - 1
}

// visibleWindow picks which lines to show so the cursor stays on screen, with
// one line of context above it where possible.
func visibleWindow(lines []lineRange, cursorLine, maxLines int) (top, bottom int) {
	if maxLines <= 0 || maxLines >= len(lines) {
		return 0, len(lines)
	}

	top = cursorLine - 1
	if top < 0 {
		top = 0
	}
	if max := len(lines) - maxLines; top > max {
		top = max
	}
	return top, top + maxLines
}

// cellAt decides how to draw one character of the passage.
func cellAt(sess *typing.Session, i int) (cellKind, rune) {
	ch := sess.CharAt(i)

	kind := cellUntyped
	switch ch.State {
	case typing.Correct:
		kind = cellCorrect
	case typing.Incorrect:
		kind = cellIncorrect
	}

	// Show the target character, so the passage stays readable even where the
	// user got it wrong. The exception is typing something over an expected
	// space: rendering the space would make the mistake invisible, so show
	// what they actually typed.
	r := ch.Target
	if ch.State == typing.Incorrect && ch.Target == ' ' && ch.Typed != ' ' {
		r = ch.Typed
	}

	// Once the session is finished Pos is past the end, so no cursor is drawn.
	if i == sess.Pos() {
		kind = cellCursor
	}
	return kind, r
}

// renderPassage draws the passage with per-character feedback, wrapped to
// width and limited to maxLines of scrolling window. A maxLines of zero shows
// every line.
func renderPassage(s Styles, sess *typing.Session, width, maxLines int) string {
	target := []rune(sess.Target())
	if len(target) == 0 {
		return ""
	}

	lines := wrapLines(target, width)
	top, bottom := visibleWindow(lines, lineOf(lines, sess.Pos()), maxLines)

	var out strings.Builder
	for li := top; li < bottom; li++ {
		if li > top {
			out.WriteByte('\n')
		}
		writeLine(&out, s, sess, lines[li])
	}
	return out.String()
}

// writeLine emits one display line, coalescing consecutive characters that
// share a style into a single styled run.
func writeLine(out *strings.Builder, s Styles, sess *typing.Session, ln lineRange) {
	var (
		run     strings.Builder
		runKind cellKind
		open    bool
	)
	flush := func() {
		if open {
			out.WriteString(runKind.style(s).Render(run.String()))
			run.Reset()
			open = false
		}
	}

	for i := ln.start; i < ln.end; i++ {
		kind, r := cellAt(sess, i)
		if open && kind != runKind {
			flush()
		}
		runKind, open = kind, true
		run.WriteRune(r)
	}
	flush()
}
