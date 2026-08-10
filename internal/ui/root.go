package ui

import (
	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/lobby"
	"github.com/Pikaryu729/typesafe/internal/store"
)

// Config describes one SSH session to the UI.
type Config struct {
	Username string
	PlayerID string
	Store    *lobby.Store
	// User is the account this session's public key resolved to. A zero User
	// means the session is anonymous — no database, or the lookup failed — and
	// nothing it does will be recorded.
	User store.User
	// Fingerprint identifies this session's public key, so it can be attached
	// to another account from the link screen.
	Fingerprint string
	// Repo persists accounts and runs. Nil is valid and means the app runs
	// exactly as it did before there was a database.
	Repo store.Repository
	// Renderer must be scoped to this session's terminal, not the server's.
	Renderer      *lipgloss.Renderer
	Width, Height int
}

// Root is the top-level model for one SSH session. It owns the session
// context, keeps exactly one Screen active, and swaps it on navigateMsg.
type Root struct {
	ctx    *Context
	screen Screen
}

// NewRoot builds the model for a session.
func NewRoot(cfg Config) Root {
	ctx := &Context{
		Username:    cfg.Username,
		PlayerID:    cfg.PlayerID,
		Store:       cfg.Store,
		User:        cfg.User,
		Fingerprint: cfg.Fingerprint,
		Repo:        cfg.Repo,
		Styles:      NewStyles(cfg.Renderer),
		Width:       cfg.Width,
		Height:      cfg.Height,
	}
	return Root{ctx: ctx, screen: NewMenu(ctx)}
}

// Context returns the session context. Callers use it to attach the Bubble Tea
// program with SetSender once the program exists.
func (m Root) Context() *Context { return m.ctx }

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
