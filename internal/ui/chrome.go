package ui

import "strings"

// Layout fragments every screen shares, so headers and help lines stay
// consistent without each screen re-deriving them.

// header renders the wordmark with an optional line of context beneath it.
func (c *Context) header(subtitle string) string {
	var b strings.Builder
	b.WriteString(c.Styles.Title.Render("typesafe"))
	if subtitle != "" {
		b.WriteString("\n")
		b.WriteString(c.Styles.Subtitle.Render(subtitle))
	}
	return b.String()
}

// help renders key hints joined by a separator, e.g.
//
//	↑/↓ move · enter select · q quit
func (c *Context) help(hints ...string) string {
	return c.Styles.Help.Render(strings.Join(hints, " · "))
}

// minContentWidth keeps layout sane on absurdly narrow terminals rather than
// producing zero-width or negative wrapping.
const minContentWidth = 20

// contentWidth is the space a screen actually has, once the app's own padding
// is taken out.
func (c *Context) contentWidth() int {
	if w := c.Width - 2*appPaddingX; w > minContentWidth {
		return w
	}
	return minContentWidth
}

// Bounds on how much of a passage is shown at once. Below the minimum there is
// no room to see what is coming; above the maximum the eye has too far to
// travel between lines.
const (
	minPassageLines = 2
	maxPassageLines = 6
	// passageChrome is the rows a typing screen spends on everything that is
	// not the passage: header, stats, help and the blank lines between them.
	passageChrome = 10
)

// passageLines is how many lines of the passage to show, given the terminal's
// height.
func (c *Context) passageLines() int {
	n := c.Height - passageChrome
	if n < minPassageLines {
		return minPassageLines
	}
	if n > maxPassageLines {
		return maxPassageLines
	}
	return n
}

// stat renders one figure with its label, e.g. "82 wpm".
func (c *Context) stat(value, label string) string {
	return c.Styles.StatValue.Render(value) + " " + c.Styles.StatLabel.Render(label)
}

// stats joins stat pairs into a single row.
func (c *Context) statRow(pairs ...string) string {
	return strings.Join(pairs, "   ")
}
