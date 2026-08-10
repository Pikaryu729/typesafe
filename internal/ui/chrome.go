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
