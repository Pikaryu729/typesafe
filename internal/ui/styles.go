package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/Pikaryu729/typesafe/internal/cosmetics"
)

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
	currency  lipgloss.Color // byte totals and prices
}{
	accent:    lipgloss.Color("212"),
	text:      lipgloss.Color("252"),
	muted:     lipgloss.Color("245"),
	dim:       lipgloss.Color("240"),
	danger:    lipgloss.Color("203"),
	success:   lipgloss.Color("78"),
	cursorFg:  lipgloss.Color("235"),
	highlight: lipgloss.Color("212"),
	currency:  lipgloss.Color("220"),
}

// Styles holds every lipgloss style the UI uses.
//
// It must be built per SSH session with NewStyles. Lipgloss's package-level
// renderer detects the *server's* terminal, not the client's, so using it
// would give every connected user the server's colour profile. There are
// deliberately no package-level styles here so that mistake is not available.
type Styles struct {
	// renderer is kept so styles that depend on what a typist has equipped —
	// a bought name colour, a bought passage theme — can be built from the
	// same session-scoped renderer rather than from the package-level one.
	// It is unexported for the reason above: there is no way to reach a
	// renderer here that is not this session's.
	renderer *lipgloss.Renderer

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

	// Bytes and the shop
	Currency lipgloss.Style // a byte total or a price
	Equipped lipgloss.Style // the cosmetic currently worn in a slot
	Locked   lipgloss.Style // something owned but not affordable yet
}

// Padding applied by Styles.App around every screen. Screens that need to know
// how much room they actually have should use Context.contentWidth rather than
// subtracting these themselves.
const (
	appPaddingX = 2
	appPaddingY = 1
)

// NewStyles builds the style set against a session-scoped renderer, which
// callers get from newRenderer(sshSession) in cmd/server.
func NewStyles(r *lipgloss.Renderer) Styles {
	return Styles{
		renderer: r,

		App:      r.NewStyle().Padding(appPaddingY, appPaddingX),
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

		Currency: r.NewStyle().Foreground(palette.currency).Bold(true),
		Equipped: r.NewStyle().Foreground(palette.success),
		Locked:   r.NewStyle().Foreground(palette.dim),
	}
}

// Name renders a player's name in whatever colour they have bought, falling
// back to fallback for everyone who has not bought one.
//
// The style is built here rather than looked up because a name colour is a
// purchase, not one of the app's own: there is no fixed set of them to hold on
// Styles. It still comes from this session's renderer, which is why the
// renderer is kept — reaching for the package-level one is the mistake this
// whole type exists to prevent.
func (s Styles) Name(f cosmetics.Flair, fallback lipgloss.Style) lipgloss.Style {
	colour, ok := f.NameColor()
	if !ok {
		return fallback
	}
	return s.renderer.NewStyle().Foreground(lipgloss.Color(colour))
}

// Themed returns a copy with the passage colours a bought theme asks for. An
// empty or unknown ID returns the styles unchanged, which is what everyone who
// has not bought a theme gets.
func (s Styles) Themed(themeID string) Styles {
	t := cosmetics.ThemeOf(themeID)
	if t == (cosmetics.Theme{}) {
		return s
	}

	r := s.renderer
	s.Untyped = r.NewStyle().Foreground(lipgloss.Color(t.Untyped))
	s.Correct = r.NewStyle().Foreground(lipgloss.Color(t.Correct))
	s.Incorrect = r.NewStyle().Foreground(lipgloss.Color(t.Incorrect)).Underline(true)
	s.Cursor = r.NewStyle().
		Foreground(lipgloss.Color(t.CursorFg)).
		Background(lipgloss.Color(t.CursorBg))
	return s
}
