package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/economy"
	"github.com/Pikaryu729/typesafe/internal/lobby"
)

// RaceResults shows the final standings, what the race paid, and offers a
// rematch.
type RaceResults struct {
	ctx     *Context
	lobby   *lobby.Lobby
	results []lobby.Result
	award   economy.Award
	snap    lobby.Snapshot
	err     string
}

// NewRaceResults shows the standings from a finished race and the bytes it
// earned. A zero award — an anonymous session, or a race this player did not
// finish — renders no breakdown at all.
func NewRaceResults(ctx *Context, l *lobby.Lobby, results []lobby.Result, award economy.Award) RaceResults {
	return RaceResults{ctx: ctx, lobby: l, results: results, award: award, snap: l.Snapshot()}
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

	case "s":
		// Spending what you just earned means leaving the lobby, which is
		// deliberate: cosmetics are picked up by the lobby when you join it,
		// so shopping from inside one would leave everyone else looking at
		// what you were wearing before.
		if !r.ctx.tracking() {
			return r, nil
		}
		r.ctx.leaveLobby(r.lobby)
		return r, navigate(NewShop(r.ctx))

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

	if earnings := r.renderEarnings(); earnings != "" {
		out.WriteString("\n")
		out.WriteString(earnings)
		out.WriteString("\n")
	}

	if r.err != "" {
		out.WriteString("\n")
		out.WriteString(s.Error.Render(r.err))
		out.WriteString("\n")
	}

	out.WriteString("\n")
	hints := []string{}
	if r.isHost() {
		hints = append(hints, "enter rematch")
	} else {
		out.WriteString(s.Subtitle.Render("waiting for the host to call a rematch"))
		out.WriteString("\n")
	}
	if r.ctx.tracking() {
		hints = append(hints, "s shop")
	}
	out.WriteString(r.ctx.help(append(hints, "esc leave")...))
	return out.String()
}

// renderEarnings is the breakdown of what this race paid, under a line saying
// where the player came. A race they did not finish pays nothing and says so
// rather than showing an empty table.
func (r RaceResults) renderEarnings() string {
	if r.award.Empty() {
		return ""
	}

	var b strings.Builder
	if place := r.ownPlace(); place > 0 {
		b.WriteString(r.ctx.Styles.Subtitle.Render("you placed " + ordinal(place)))
		b.WriteString("\n\n")
	}
	b.WriteString(renderAward(r.ctx, r.award))
	return b.String()
}

func (r RaceResults) ownPlace() int {
	for _, res := range r.results {
		if res.PlayerID == r.ctx.PlayerID {
			return res.Place
		}
	}
	return 0
}

// rowIndent matches the left padding Styles.Item applies. A standings row is
// built from several styles rather than one, so the indent is written here
// instead: rendering Item three times across a line would indent it three
// times.
const rowIndent = "  "

func (r RaceResults) renderResult(res lobby.Result) string {
	s := r.ctx.Styles
	name := padTo(renderName(s, res.Name, res.Flair, res.PlayerID == r.ctx.PlayerID), nameWidth)

	if !res.Finished {
		return rowIndent + s.StatLabel.Render(fmt.Sprintf("%-4s ", "—")) +
			name + " " + s.Subtitle.Render("did not finish")
	}

	return rowIndent +
		s.StatValue.Render(fmt.Sprintf("%-4s ", ordinal(res.Place))) +
		name +
		s.StatLabel.Render(fmt.Sprintf(" %3.0f wpm   %s acc   %s",
			res.WPM,
			formatAccuracy(res.Accuracy),
			formatDuration(res.Elapsed)))
}
