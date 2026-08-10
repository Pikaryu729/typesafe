package ui

import (
	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

// Root is the top-level model for one SSH session. It owns the session
// context, keeps exactly one Screen active, and swaps it on navigateMsg.
type Root struct {
	ctx    *Context
	screen Screen
}

// NewRoot builds the model for a session. renderer must be scoped to that
// session; width and height are the client's initial terminal size.
func NewRoot(username string, renderer *lipgloss.Renderer, width, height int) Root {
	ctx := &Context{
		Username: username,
		Styles:   NewStyles(renderer),
		Width:    width,
		Height:   height,
	}
	return Root{ctx: ctx, screen: NewMenu(ctx)}
}

func (m Root) Init() tea.Cmd { return m.screen.Init() }

func (m Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// The context is shared by pointer, so every screen — current and
		// future — sees the new size without being told individually.
		m.ctx.Width, m.ctx.Height = msg.Width, msg.Height

	case tea.KeyMsg:
		// Ctrl+C always disconnects, whatever the active screen is doing.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case navigateMsg:
		m.screen = msg.to
		return m, m.screen.Init()
	}

	screen, cmd := m.screen.Update(msg)
	m.screen = screen
	return m, cmd
}

func (m Root) View() string {
	return m.ctx.Styles.App.Render(m.screen.View())
}
