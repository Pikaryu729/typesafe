package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikary729/typesafe/internal/lobby"
)

// WaitingRoom is the pre-race screen: who is here, who is ready, and the
// countdown once the host starts.
type WaitingRoom struct {
	ctx   *Context
	lobby *lobby.Lobby
	snap  lobby.Snapshot

	// countdown is the seconds left before the race opens, or zero when no
	// countdown is running. It comes from the lobby rather than a local timer,
	// so every player counts in together.
	countdown int
	err       string
}

// NewWaitingRoom returns the room for a lobby the player has already joined.
func NewWaitingRoom(ctx *Context, l *lobby.Lobby) WaitingRoom {
	return WaitingRoom{ctx: ctx, lobby: l, snap: l.Snapshot()}
}

func (w WaitingRoom) Init() tea.Cmd {
	// Start forwarding this lobby's events into the program. The subscription
	// ends by itself when we leave or the lobby closes.
	w.ctx.subscribe(w.lobby)
	return nil
}

func (w WaitingRoom) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case lobby.LobbyUpdated:
		w.snap = msg.Snapshot
		if w.snap.Phase == lobby.PhaseWaiting {
			w.countdown = 0 // a cancelled countdown clears the number
		}
		return w, nil

	case lobby.CountdownTick:
		w.countdown = msg.SecondsLeft
		return w, nil

	case lobby.RaceStarted:
		return w, navigate(NewRace(w.ctx, w.lobby, msg))

	case tea.KeyMsg:
		return w.handleKey(msg)
	}
	return w, nil
}

func (w WaitingRoom) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		w.ctx.leaveLobby(w.lobby)
		return w, navigate(NewBrowser(w.ctx))

	case "r", " ":
		me, ok := w.me()
		if ok {
			w.lobby.SetReady(w.ctx.PlayerID, !me.Ready)
		}
		w.err = ""

	case "enter":
		if err := w.lobby.Start(w.ctx.PlayerID); err != nil {
			w.err = startHint(err)
		} else {
			w.err = ""
		}
	}
	return w, nil
}

// startHint turns a refused start into something worth reading on screen.
func startHint(err error) string {
	switch err {
	case lobby.ErrNotHost:
		return "only the host can start the race"
	case lobby.ErrNotReady:
		return "waiting for everyone to ready up"
	default:
		return err.Error()
	}
}

func (w WaitingRoom) me() (lobby.PlayerState, bool) {
	for _, p := range w.snap.Players {
		if p.ID == w.ctx.PlayerID {
			return p, true
		}
	}
	return lobby.PlayerState{}, false
}

func (w WaitingRoom) isHost() bool { return w.snap.HostID == w.ctx.PlayerID }

func (w WaitingRoom) View() string {
	s := w.ctx.Styles

	var out strings.Builder
	out.WriteString(w.ctx.header("lobby " + w.snap.Code))
	out.WriteString("\n\n")

	for _, p := range w.snap.Players {
		out.WriteString(w.renderPlayer(p))
		out.WriteString("\n")
	}

	out.WriteString("\n")
	switch {
	case w.countdown > 0:
		out.WriteString(s.StatValue.Render(fmt.Sprintf("starting in %d…", w.countdown)))
	case w.err != "":
		out.WriteString(s.Error.Render(w.err))
	case len(w.snap.Players) < 2:
		out.WriteString(s.Subtitle.Render("waiting for another player — share code " + w.snap.Code))
	case w.snap.AllReady() && w.isHost():
		out.WriteString(s.Good.Render("everyone is ready — press enter to start"))
	case w.snap.AllReady():
		out.WriteString(s.Subtitle.Render("everyone is ready — waiting for the host"))
	default:
		out.WriteString(s.Subtitle.Render("waiting for players to ready up"))
	}

	out.WriteString("\n\n")
	hints := []string{"r ready", "esc leave"}
	if w.isHost() {
		hints = append([]string{"enter start"}, hints...)
	}
	out.WriteString(w.ctx.help(hints...))
	return out.String()
}

func (w WaitingRoom) renderPlayer(p lobby.PlayerState) string {
	s := w.ctx.Styles

	mark := s.Subtitle.Render("○")
	if p.Ready {
		mark = s.Good.Render("●")
	}

	name := p.Name
	if p.ID == w.ctx.PlayerID {
		name += " (you)"
	}
	if p.ID == w.snap.HostID {
		name += " · host"
	}

	return s.Item.Render(mark + " " + name)
}
