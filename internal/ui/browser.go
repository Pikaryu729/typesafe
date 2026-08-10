package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikary729/typesafe/internal/lobby"
)

// refreshInterval is how often the browser re-reads the lobby list.
//
// Lobbies come and go in other sessions, and there is no server-wide
// subscription to hear about it — only per-lobby ones. Polling a map read once
// a second is cheaper than maintaining a global fan-out for a list that is
// only on screen while someone is looking at it.
const refreshInterval = time.Second

// refreshMsg asks the browser to re-read the lobby list.
type refreshMsg time.Time

func scheduleRefresh() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

// Browser lists open lobbies and offers ways into one.
type Browser struct {
	ctx     *Context
	lobbies []lobby.Snapshot
	cursor  int

	// entering a join code
	typingCode bool
	code       string

	err string
}

// NewBrowser returns the lobby list.
func NewBrowser(ctx *Context) Browser {
	b := Browser{ctx: ctx}
	b.lobbies = ctx.Store.List()
	return b
}

func (b Browser) Init() tea.Cmd { return scheduleRefresh() }

func (b Browser) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case refreshMsg:
		b.lobbies = b.ctx.Store.List()
		b.clampCursor()
		return b, scheduleRefresh()

	case tea.KeyMsg:
		if b.typingCode {
			return b.handleCodeKey(msg)
		}
		return b.handleKey(msg)
	}
	return b, nil
}

func (b Browser) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		return b, navigate(NewMenu(b.ctx))

	case "up", "k":
		if b.cursor > 0 {
			b.cursor--
		}
	case "down", "j":
		if b.cursor < len(b.lobbies)-1 {
			b.cursor++
		}

	case "c":
		return b.create()

	case "/":
		b.typingCode, b.code, b.err = true, "", ""

	case "r":
		b.lobbies = b.ctx.Store.List()
		b.clampCursor()

	case "enter":
		if len(b.lobbies) == 0 {
			return b.create() // nothing to join, so make one
		}
		return b.join(b.lobbies[b.cursor].Code)
	}
	return b, nil
}

// handleCodeKey drives the inline join-code prompt.
func (b Browser) handleCodeKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		b.typingCode, b.code = false, ""
	case tea.KeyEnter:
		b.typingCode = false
		return b.join(b.code)
	case tea.KeyBackspace:
		if b.code != "" {
			b.code = b.code[:len(b.code)-1]
		}
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			if len(b.code) < codeLength {
				b.code += strings.ToUpper(string(r))
			}
		}
	}
	return b, nil
}

// codeLength mirrors the store's join-code length, so the prompt stops at the
// right width.
const codeLength = 4

func (b Browser) create() (Screen, tea.Cmd) {
	l := b.ctx.Store.Create(b.ctx.PlayerID, b.ctx.Username)
	return b, navigate(NewWaitingRoom(b.ctx, l))
}

func (b Browser) join(code string) (Screen, tea.Cmd) {
	l, err := b.ctx.Store.Get(code)
	if err != nil {
		b.err = fmt.Sprintf("no lobby with code %s", strings.ToUpper(strings.TrimSpace(code)))
		return b, nil
	}
	if err := l.Join(b.ctx.PlayerID, b.ctx.Username); err != nil {
		b.err = err.Error()
		return b, nil
	}
	return b, navigate(NewWaitingRoom(b.ctx, l))
}

// clampCursor keeps the selection valid as lobbies appear and disappear.
func (b *Browser) clampCursor() {
	if b.cursor >= len(b.lobbies) {
		b.cursor = max(0, len(b.lobbies)-1)
	}
}

func (b Browser) View() string {
	s := b.ctx.Styles

	var out strings.Builder
	out.WriteString(b.ctx.header("lobbies"))
	out.WriteString("\n\n")

	switch {
	case b.typingCode:
		out.WriteString(s.Subtitle.Render("join code: "))
		out.WriteString(s.StatValue.Render(b.code + strings.Repeat("_", codeLength-len(b.code))))
		out.WriteString("\n")
	case len(b.lobbies) == 0:
		out.WriteString(s.Subtitle.Render("no open lobbies — press c to start one"))
		out.WriteString("\n")
	default:
		out.WriteString(b.renderList())
	}

	if b.err != "" {
		out.WriteString("\n")
		out.WriteString(s.Error.Render(b.err))
		out.WriteString("\n")
	}

	out.WriteString("\n")
	if b.typingCode {
		out.WriteString(b.ctx.help("enter join", "esc cancel"))
	} else {
		out.WriteString(b.ctx.help("↑/↓ move", "enter join", "c create", "/ by code", "esc menu"))
	}
	return out.String()
}

func (b Browser) renderList() string {
	s := b.ctx.Styles

	var out strings.Builder
	for i, l := range b.lobbies {
		host := "—"
		if h, ok := l.Host(); ok {
			host = h.Name
		}
		line := fmt.Sprintf("%s  %-16s %d/%d  %s",
			l.Code, truncate(host, 16), len(l.Players), lobby.MaxPlayers, l.Phase)

		if i == b.cursor {
			out.WriteString(s.SelectedItem.Render("> " + line))
		} else {
			out.WriteString(s.Item.Render(line))
		}
		out.WriteString("\n")
	}
	return out.String()
}

// truncate shortens s to n characters, marking where it was cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
