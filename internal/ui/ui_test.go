package ui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Pikaryu729/typesafe/internal/lobby"
)

// testRenderer returns a renderer writing nowhere. Its colour profile degrades
// to plain ASCII, which keeps View output free of escape codes and therefore
// assertable.
func testRenderer() *lipgloss.Renderer { return lipgloss.NewRenderer(io.Discard) }

func newTestContext() *Context {
	return &Context{
		Username: "tester",
		PlayerID: "tester-id",
		Store:    lobby.NewStore(),
		Styles:   NewStyles(testRenderer()),
		Width:    80,
		Height:   24,
	}
}

func newTestRoot() Root {
	return NewRoot(Config{
		Username: "tester",
		PlayerID: "tester-id",
		Store:    lobby.NewStore(),
		Renderer: testRenderer(),
		Width:    80,
		Height:   24,
	})
}

// key builds the KeyMsg a terminal would produce for a named key.
func key(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}

// send drives a screen through a sequence of keys, returning the final screen
// and the command from the last key.
func send(s Screen, keys ...string) (Screen, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		s, cmd = s.Update(key(k))
	}
	return s, cmd
}

func TestMenuStartsOnFirstItem(t *testing.T) {
	m := NewMenu(newTestContext())
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
}

func TestMenuNavigation(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want int
	}{
		{"down", []string{"down"}, 1},
		{"vim down", []string{"j"}, 1},
		{"up wraps to the end", []string{"up"}, len(menuItems) - 1},
		{"down wraps to the start", strings.Split(strings.Repeat("j", len(menuItems)), ""), 0},
		{"home", []string{"j", "j", "g"}, 0},
		{"end", []string{"G"}, len(menuItems) - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := send(NewMenu(newTestContext()), tt.keys...)
			if got := s.(Menu).cursor; got != tt.want {
				t.Errorf("after %v cursor = %d, want %d", tt.keys, got, tt.want)
			}
		})
	}
}

func TestMenuEnterNavigatesToAScreen(t *testing.T) {
	_, cmd := send(NewMenu(newTestContext()), "enter") // Practice
	if cmd == nil {
		t.Fatal("selecting Practice produced no command")
	}
	if _, ok := cmd().(navigateMsg); !ok {
		t.Errorf("selecting Practice produced %T, want navigateMsg", cmd())
	}
}

func TestMenuQuitItemQuits(t *testing.T) {
	keys := append(strings.Split(strings.Repeat("j", len(menuItems)-1), ""), "enter")
	_, cmd := send(NewMenu(newTestContext()), keys...)

	if cmd == nil {
		t.Fatal("selecting Quit produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("selecting Quit produced %T, want tea.QuitMsg", cmd())
	}
}

func TestMenuQKeyQuits(t *testing.T) {
	_, cmd := send(NewMenu(newTestContext()), "q")
	if cmd == nil {
		t.Fatal("q produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q produced %T, want tea.QuitMsg", cmd())
	}
}

func TestMenuViewListsEveryItem(t *testing.T) {
	view := NewMenu(newTestContext()).View()

	for _, item := range menuItems {
		if !strings.Contains(view, item.title) {
			t.Errorf("view is missing menu item %q:\n%s", item.title, view)
		}
	}
	if !strings.Contains(view, "tester") {
		t.Errorf("view does not show the username:\n%s", view)
	}
}

func TestRootStartsOnTheMenu(t *testing.T) {
	if _, ok := newTestRoot().screen.(Menu); !ok {
		t.Errorf("initial screen is %T, want Menu", newTestRoot().screen)
	}
}

func TestRootSwitchesScreenOnNavigate(t *testing.T) {
	root := newTestRoot()
	target := NewBrowser(root.ctx)

	updated, _ := root.Update(navigateMsg{to: target})

	if _, ok := updated.(Root).screen.(Browser); !ok {
		t.Errorf("screen = %T, want the navigated-to Browser", updated.(Root).screen)
	}
}

func TestRootCtrlCQuitsFromAnyScreen(t *testing.T) {
	root := newTestRoot()
	root.screen = NewPractice(root.ctx) // a screen that consumes ordinary keys

	_, cmd := root.Update(key("ctrl+c"))

	if cmd == nil {
		t.Fatal("ctrl+c produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c produced %T, want tea.QuitMsg", cmd())
	}
}

// The context is shared by pointer so a resize reaches every screen, including
// ones built later, without the router notifying each one.
func TestRootResizeUpdatesSharedContext(t *testing.T) {
	root := newTestRoot()

	updated, _ := root.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	ctx := updated.(Root).ctx
	if ctx.Width != 120 || ctx.Height != 40 {
		t.Errorf("context size = %dx%d, want 120x40", ctx.Width, ctx.Height)
	}
	if root.ctx.Width != 120 {
		t.Error("the original root's context did not see the resize")
	}
}

func TestRootForwardsKeysToTheActiveScreen(t *testing.T) {
	root := newTestRoot()

	updated, _ := root.Update(key("down"))

	if got := updated.(Root).screen.(Menu).cursor; got != 1 {
		t.Errorf("menu cursor = %d after forwarding down, want 1", got)
	}
}
