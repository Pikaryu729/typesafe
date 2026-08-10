// Package ui contains the Bubble Tea models that make up a session's terminal
// interface: a root router plus one model per screen.
//
// Screens run on a single SSH session's goroutine and own only session-local
// state. Anything shared between connected users lives in the lobby store and
// reaches a screen as a message — see Context.subscribe.
package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikary729/typesafe/internal/lobby"
)

// Context is the per-connection state every screen needs. One is created per
// SSH session and passed to each screen the router builds.
type Context struct {
	// Username is the name the client connected with, used as a display name.
	Username string
	// PlayerID identifies this session in the lobby store. It is not the
	// username: any key is accepted, so two people can connect under the same
	// name and one person can hold several sessions.
	PlayerID string
	// Store is the server-wide lobby registry, shared by every session.
	Store *lobby.Store
	// Styles is scoped to this session's terminal. See NewStyles.
	Styles Styles
	// Width and Height track the client's terminal, updated on resize.
	Width, Height int

	// send delivers a message into this session's Bubble Tea program from
	// outside its update loop. See SetSender.
	send func(tea.Msg)
}

// SetSender wires the context to its Bubble Tea program, so lobby events can
// be pushed in from other goroutines.
//
// It must be called after tea.NewProgram and before the program runs. That
// ordering is what makes the unsynchronised field safe: the assignment and
// every later read are separated by the start of the program's loop, on the
// same goroutine that performs both.
func (c *Context) SetSender(send func(tea.Msg)) { c.send = send }

// Send delivers a message to this session's program. It is safe to call from
// any goroutine, and does nothing if no program is attached — which is the
// case in tests.
func (c *Context) Send(msg tea.Msg) {
	if c.send != nil {
		c.send(msg)
	}
}

// subscribe starts forwarding a lobby's events into this session's program.
//
// A Bubble Tea View cannot read shared state directly, so updates have to
// arrive as messages. The goroutine ends on its own when the channel closes,
// which the lobby does when the player leaves or the lobby shuts down — so
// there is nothing to cancel and nothing to leak.
func (c *Context) subscribe(l *lobby.Lobby) {
	ch := l.Subscribe(c.PlayerID)
	go func() {
		for ev := range ch {
			c.Send(ev)
		}
	}()
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
