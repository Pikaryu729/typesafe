package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikary729/typesafe/internal/lobby"
)

// RaceResults shows the final standings and offers a rematch.
type RaceResults struct {
	ctx     *Context
	lobby   *lobby.Lobby
	results []lobby.Result
	snap    lobby.Snapshot
	err     string
}

// NewRaceResults shows the standings from a finished race.
func NewRaceResults(ctx *Context, l *lobby.Lobby, results []lobby.Result) RaceResults {
	return RaceResults{ctx: ctx, lobby: l, results: results, snap: l.Snapshot()}
}

func (r RaceResults) Init() tea.Cmd { return nil }

func (r RaceResults) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case lobby.LobbyUpdated:
		r.snap = msg.Snapshot
		// The host calling a rematch puts the lobby back into waiting, which
		// is how everyone else learns to follow them to the room.
		if r.snap.Phase == lobby.PhaseWaiting {
			return r, navigate(NewWaitingRoom(r.ctx, r.lobby))
		}
		return r, nil

	case tea.KeyMsg:
		return r.handleKey(msg)
	}
	return r, nil
}

func (r RaceResults) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		r.ctx.leaveLobby(r.lobby)
		return r, navigate(NewBrowser(r.ctx))

	case "enter", "r":
		if err := r.lobby.Rematch(r.ctx.PlayerID); err != nil {
			if err == lobby.ErrNotHost {
				r.err = "only the host can start a rematch"
			} else {
				r.err = err.Error()
			}
			return r, nil
		}
		// The resulting LobbyUpdated moves everyone, including us.
		return r, nil
	}
	return r, nil
}

func (r RaceResults) isHost() bool { return r.snap.HostID == r.ctx.PlayerID }

func (r RaceResults) View() string {
	s := r.ctx.Styles

	var out strings.Builder
	out.WriteString(r.ctx.header("race over · lobby " + r.snap.Code))
	out.WriteString("\n\n")

	for _, res := range r.results {
		out.WriteString(r.renderResult(res))
		out.WriteString("\n")
	}

	if r.err != "" {
		out.WriteString("\n")
		out.WriteString(s.Error.Render(r.err))
		out.WriteString("\n")
	}

	out.WriteString("\n")
	if r.isHost() {
		out.WriteString(r.ctx.help("enter rematch", "esc leave"))
	} else {
		out.WriteString(s.Subtitle.Render("waiting for the host to call a rematch"))
		out.WriteString("\n")
		out.WriteString(r.ctx.help("esc leave"))
	}
	return out.String()
}

func (r RaceResults) renderResult(res lobby.Result) string {
	s := r.ctx.Styles

	name := res.Name
	if res.PlayerID == r.ctx.PlayerID {
		name += " (you)"
	}

	if !res.Finished {
		return s.Item.Render(fmt.Sprintf("%-4s %-18s %s",
			"—", truncate(name, 18), s.Subtitle.Render("did not finish")))
	}

	place := s.StatValue.Render(fmt.Sprintf("%-4s", ordinal(res.Place)))
	return s.Item.Render(fmt.Sprintf("%s %-18s %3.0f wpm   %s acc   %s",
		place,
		truncate(name, 18),
		res.WPM,
		formatAccuracy(res.Accuracy),
		formatDuration(res.Elapsed)))
}
