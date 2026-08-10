package ui

import "github.com/charmbracelet/lipgloss"

// Palette is the app's colour set, kept in one place so screens agree.
//
// Values are ANSI256 indices, which is the profile the SSH middleware forces
// as a minimum.
var palette = struct {
	accent    lipgloss.Color // brand colour, used for emphasis and selection
	text      lipgloss.Color // typed-correct text and headings
	muted     lipgloss.Color // labels, help lines, untyped text
	dim       lipgloss.Color // the faintest readable tone
	danger    lipgloss.Color // mistakes and errors
	success   lipgloss.Color // finished, ready, correct
	cursorFg  lipgloss.Color // text under the cursor block
	highlight lipgloss.Color // cursor block background
}{
	accent:    lipgloss.Color("212"),
	text:      lipgloss.Color("252"),
	muted:     lipgloss.Color("245"),
	dim:       lipgloss.Color("240"),
	danger:    lipgloss.Color("203"),
	success:   lipgloss.Color("78"),
	cursorFg:  lipgloss.Color("235"),
	highlight: lipgloss.Color("212"),
}

// Styles holds every lipgloss style the UI uses.
//
// It must be built per SSH session with NewStyles. Lipgloss's package-level
// renderer detects the *server's* terminal, not the client's, so using it
// would give every connected user the server's colour profile. There are
// deliberately no package-level styles here so that mistake is not available.
type Styles struct {
	// Chrome
	App      lipgloss.Style // outer padding around every screen
	Title    lipgloss.Style // the "typesafe" wordmark
	Subtitle lipgloss.Style // a line of context under a title
	Help     lipgloss.Style // the key hints along the bottom
	Error    lipgloss.Style

	// Menus
	Item         lipgloss.Style
	SelectedItem lipgloss.Style

	// Passage rendering
	Untyped   lipgloss.Style // not yet reached
	Correct   lipgloss.Style // typed and right
	Incorrect lipgloss.Style // typed and wrong
	Cursor    lipgloss.Style // the character under the cursor

	// Stats
	StatValue lipgloss.Style
	StatLabel lipgloss.Style
	Good      lipgloss.Style
	Bad       lipgloss.Style
}

// NewStyles builds the style set against a session-scoped renderer, which
// callers get from bubbletea.MakeRenderer(sshSession).
func NewStyles(r *lipgloss.Renderer) Styles {
	return Styles{
		App:      r.NewStyle().Padding(1, 2),
		Title:    r.NewStyle().Foreground(palette.accent).Bold(true),
		Subtitle: r.NewStyle().Foreground(palette.muted),
		Help:     r.NewStyle().Foreground(palette.dim),
		Error:    r.NewStyle().Foreground(palette.danger),

		Item:         r.NewStyle().Foreground(palette.muted).PaddingLeft(2),
		SelectedItem: r.NewStyle().Foreground(palette.accent).Bold(true).PaddingLeft(0),

		Untyped:   r.NewStyle().Foreground(palette.dim),
		Correct:   r.NewStyle().Foreground(palette.text),
		Incorrect: r.NewStyle().Foreground(palette.danger).Underline(true),
		Cursor:    r.NewStyle().Foreground(palette.cursorFg).Background(palette.highlight),

		StatValue: r.NewStyle().Foreground(palette.accent).Bold(true),
		StatLabel: r.NewStyle().Foreground(palette.muted),
		Good:      r.NewStyle().Foreground(palette.success),
		Bad:       r.NewStyle().Foreground(palette.danger),
	}
}
