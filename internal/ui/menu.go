package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/economy"
)

// menuItem is one selectable entry on the main menu.
type menuItem struct {
	title string
	desc  string
	// act runs when the item is chosen. Returning nil leaves the menu up.
	act func(*Context) tea.Cmd
	// needsAccount marks an item that only means something when this session's
	// runs are being recorded. Such items stay on the menu when they are not,
	// dimmed and labelled: a typist who expected a profile should be told why
	// there isn't one, not left to conclude the app has none.
	needsAccount bool
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
		title:        "Profile",
		desc:         "Your bests, totals and recent runs",
		act:          func(c *Context) tea.Cmd { return navigate(NewProfile(c)) },
		needsAccount: true,
	},
	{
		title:        "Shop",
		desc:         "Spend " + economy.Unit + " on cosmetics",
		act:          func(c *Context) tea.Cmd { return navigate(NewShop(c)) },
		needsAccount: true,
	},
	{
		title:        "Link a device",
		desc:         "Use this account from another machine",
		act:          func(c *Context) tea.Cmd { return navigate(NewLink(c)) },
		needsAccount: true,
	},
	{
		title: "Quit",
		desc:  "Disconnect",
		act:   func(*Context) tea.Cmd { return tea.Quit },
	},
}

// enabled reports whether the item can be chosen in this session.
func (i menuItem) enabled(c *Context) bool { return !i.needsAccount || c.tracking() }

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
		if item := menuItems[m.cursor]; item.enabled(m.ctx) {
			return m, item.act(m.ctx)
		}
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m Menu) View() string {
	s := m.ctx.Styles

	// The balance rides on the header rather than a screen of its own: it is
	// the number a typist wants to glance at, and it is already in the session
	// so showing it costs no query.
	subtitle := "signed in as " + m.ctx.Username
	if m.ctx.tracking() {
		subtitle += " · " + formatBalance(m.ctx.Balance)
	}

	var b strings.Builder
	b.WriteString(m.ctx.header(subtitle))
	b.WriteString("\n\n")

	for i, item := range menuItems {
		switch {
		case !item.enabled(m.ctx):
			b.WriteString(s.Item.Render(item.title))
			b.WriteString("  ")
			b.WriteString(s.Help.Render("(no account on this server)"))
		case i == m.cursor:
			b.WriteString(s.SelectedItem.Render("> " + item.title))
			b.WriteString("  ")
			b.WriteString(s.Subtitle.Render(item.desc))
		default:
			b.WriteString(s.Item.Render(item.title))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.ctx.help("↑/↓ move", "enter select", "q quit"))
	return b.String()
}
