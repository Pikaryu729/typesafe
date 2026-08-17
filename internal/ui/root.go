package ui

import (
	"math/rand/v2"

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
	// Repo persists accounts, runs and wallets. Nil is valid and means the app
	// runs exactly as it did before there was a database.
	Repo store.Repository
	// Wallet is the balance and cosmetics this account connected with, read
	// once at login. A zero Wallet is what an anonymous session gets.
	Wallet store.Wallet
	// Renderer must be scoped to this session's terminal, not the server's.
	Renderer      *lipgloss.Renderer
	Width, Height int
	// Rand overrides this session's source for award rolls. Tests set it to
	// make an award assertable; the server leaves it nil for a random one.
	Rand *rand.Rand
}

// Root is the top-level model for one SSH session. It owns the session
// context, keeps exactly one Screen active, and swaps it on navigateMsg.
type Root struct {
	ctx    *Context
	screen Screen
}

// NewRoot builds the model for a session.
func NewRoot(cfg Config) Root {
	ctx := newContext(cfg)
	return Root{ctx: ctx, screen: NewMenu(ctx)}
}

// newContext builds the per-session state from a config.
//
// It is separate from NewRoot so tests can raise a context without a program
// around it and still get every derived field — the styles a theme was applied
// to, the award generator — rather than a half-built one that panics the first
// time a passage is finished.
func newContext(cfg Config) *Context {
	base := NewStyles(cfg.Renderer)

	rng := cfg.Rand
	if rng == nil {
		// Per session rather than package-level: the generator is touched only
		// from this session's update loop, which is what makes it safe without
		// a lock.
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}

	ctx := &Context{
		Username:    cfg.Username,
		PlayerID:    cfg.PlayerID,
		Store:       cfg.Store,
		User:        cfg.User,
		Fingerprint: cfg.Fingerprint,
		Repo:        cfg.Repo,
		Styles:      base,
		baseStyles:  base,
		Width:       cfg.Width,
		Height:      cfg.Height,
		rand:        rng,
	}
	ctx.applyWallet(cfg.Wallet)
	return ctx
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
