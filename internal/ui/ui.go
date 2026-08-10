// Package ui contains the Bubble Tea models that make up a session's terminal
// interface: a root router plus one model per screen.
//
// Everything here runs on a single SSH session's goroutine and owns only
// session-local state. Anything shared between connected users lives behind
// the lobby store instead, and reaches a screen as a message.
package ui

import tea "github.com/charmbracelet/bubbletea"

// Context is the per-connection state every screen needs. One is created per
// SSH session and passed to each screen the router builds.
type Context struct {
	// Username is the name the client connected with, used as a display name.
	Username string
	// Styles is scoped to this session's terminal. See NewStyles.
	Styles Styles
	// Width and Height track the client's terminal, updated on resize.
	Width, Height int
}

// Screen is one screen of the app.
//
// It mirrors tea.Model except that Update returns a Screen, which saves every
// caller a type assertion. Screens are values, so Update returns the updated
// copy rather than mutating in place.
type Screen interface {
	Init() tea.Cmd
	Update(tea.Msg) (Screen, tea.Cmd)
	View() string
}

// navigateMsg asks the router to make another screen active. Screens build
// their own successor, which keeps transition logic next to the screen that
// knows when to make it.
type navigateMsg struct{ to Screen }

// navigate returns a command that switches to s.
func navigate(s Screen) tea.Cmd {
	return func() tea.Msg { return navigateMsg{to: s} }
}
