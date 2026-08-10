// Package ui contains the Bubble Tea models that make up a session's terminal
// interface: a root router plus one model per screen.
//
// Screens run on a single SSH session's goroutine and own only session-local
// state. Anything shared between connected users lives in the lobby store and
// reaches a screen as a message — see Context.subscribe.
package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/lobby"
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
	// lobby is the lobby this session currently belongs to, if any. It backs
	// both the event pump and the cleanup on disconnect, so it is recorded the
	// moment the player joins rather than when a screen happens to subscribe.
	lobby *lobby.Lobby
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

// enterLobby records that this session is in l and starts forwarding the
// lobby's events into its program.
//
// A Bubble Tea View cannot read shared state directly, so updates have to
// arrive as messages. The pump goroutine ends on its own when the channel
// closes, which the lobby does when the player leaves or the lobby shuts down
// — so there is nothing to cancel and nothing to leak.
//
// Entering the same lobby again is a no-op. Screens within a lobby hand off to
// each other freely (waiting room, race, results, and back on a rematch), and
// resubscribing each time would drop events in the gap between closing one
// channel and opening the next.
//
// Callers should invoke this immediately on joining, not when a screen first
// wants events: it is also what tells the disconnect cleanup which lobby to
// remove the player from.
func (c *Context) enterLobby(l *lobby.Lobby) {
	if c.lobby == l {
		return
	}
	c.lobby = l

	ch := l.Subscribe(c.PlayerID)
	go func() {
		for ev := range ch {
			c.Send(ev)
		}
	}()
}

// leaveLobby removes the player and stops their event pump. Leaving closes the
// subscription channel, which is what ends the goroutine.
func (c *Context) leaveLobby(l *lobby.Lobby) {
	l.Leave(c.PlayerID)
	if c.lobby == l {
		c.lobby = nil
	}
}

// Disconnect releases everything the session was holding. Without it a client
// that drops — closing the terminal, losing the network, pressing ctrl+c —
// stays in its lobby forever as a player nobody can remove, and a lobby of
// nothing but ghosts never closes.
//
// It must run on the session's own goroutine once the Bubble Tea program has
// stopped. That is what makes touching session state here safe rather than a
// race against the update loop; see cmd/server.
func (c *Context) Disconnect() {
	if c.lobby != nil {
		c.leaveLobby(c.lobby)
	}
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
