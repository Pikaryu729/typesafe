package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// stub is a placeholder screen for a menu entry whose real implementation has
// not landed yet. It keeps the router's navigation path exercised end to end
// while features are still being built, and each use of it disappears as the
// corresponding screen arrives.
type stub struct {
	ctx   *Context
	title string
	body  string
}

func newStub(ctx *Context, title, body string) stub {
	return stub{ctx: ctx, title: title, body: body}
}

func (s stub) Init() tea.Cmd { return nil }

func (s stub) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "q":
			return s, navigate(NewMenu(s.ctx))
		}
	}
	return s, nil
}

func (s stub) View() string {
	var b strings.Builder
	b.WriteString(s.ctx.header(s.title))
	b.WriteString("\n\n")
	b.WriteString(s.ctx.Styles.Subtitle.Render(s.body))
	b.WriteString("\n\n")
	b.WriteString(s.ctx.help("esc back"))
	return b.String()
}
