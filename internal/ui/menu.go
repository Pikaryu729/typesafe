package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// menuItem is one selectable entry on the main menu.
type menuItem struct {
	title string
	desc  string
	// act runs when the item is chosen. Returning nil leaves the menu up.
	act func(*Context) tea.Cmd
}

// menuItems is the main menu, in display order.
var menuItems = []menuItem{
	{
		title: "Practice",
		desc:  "Type a passage at your own pace",
		act:   func(c *Context) tea.Cmd { return navigate(NewPractice(c)) },
	},
	{
		title: "Race",
		desc:  "Create or join a lobby and race other players",
		act:   func(c *Context) tea.Cmd { return navigate(NewBrowser(c)) },
	},
	{
		title: "Quit",
		desc:  "Disconnect",
		act:   func(*Context) tea.Cmd { return tea.Quit },
	},
}

// Menu is the main menu screen.
type Menu struct {
	ctx    *Context
	cursor int
}

// NewMenu returns the main menu, with the first item selected.
func NewMenu(ctx *Context) Menu {
	return Menu{ctx: ctx}
}

func (m Menu) Init() tea.Cmd { return nil }

func (m Menu) Update(msg tea.Msg) (Screen, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "up", "k":
		m.cursor = (m.cursor - 1 + len(menuItems)) % len(menuItems)
	case "down", "j":
		m.cursor = (m.cursor + 1) % len(menuItems)
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(menuItems) - 1
	case "enter", " ":
		return m, menuItems[m.cursor].act(m.ctx)
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m Menu) View() string {
	s := m.ctx.Styles

	var b strings.Builder
	b.WriteString(m.ctx.header("signed in as " + m.ctx.Username))
	b.WriteString("\n\n")

	for i, item := range menuItems {
		if i == m.cursor {
			b.WriteString(s.SelectedItem.Render("> " + item.title))
			b.WriteString("  ")
			b.WriteString(s.Subtitle.Render(item.desc))
		} else {
			b.WriteString(s.Item.Render(item.title))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.ctx.help("↑/↓ move", "enter select", "q quit"))
	return b.String()
}
