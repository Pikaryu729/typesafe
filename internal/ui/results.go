package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/typing"
)

// Results reports a finished solo attempt.
type Results struct {
	ctx   *Context
	stats typing.Stats
}

func newResults(ctx *Context, stats typing.Stats) Results {
	return Results{ctx: ctx, stats: stats}
}

func (r Results) Init() tea.Cmd { return nil }

func (r Results) Update(msg tea.Msg) (Screen, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return r, nil
	}

	switch key.String() {
	case "enter", "r":
		return r, navigate(NewPractice(r.ctx))
	case "esc", "q":
		return r, navigate(NewMenu(r.ctx))
	}
	return r, nil
}

func (r Results) View() string {
	s := r.ctx.Styles
	st := r.stats

	var b strings.Builder
	b.WriteString(r.ctx.header("finished"))
	b.WriteString("\n\n")

	// The headline figures.
	b.WriteString(r.ctx.statRow(
		r.ctx.stat(formatWPM(st.WPM), "wpm"),
		r.ctx.stat(formatAccuracy(st.Accuracy), "accuracy"),
		r.ctx.stat(formatDuration(st.Elapsed), "time"),
	))
	b.WriteString("\n\n")

	// The breakdown behind them.
	b.WriteString(r.ctx.statRow(
		s.Good.Render(fmt.Sprintf("%d correct", st.Correct)),
		s.Bad.Render(fmt.Sprintf("%d wrong", st.Incorrect)),
		s.StatLabel.Render(fmt.Sprintf("%d keystrokes", st.Keystrokes)),
		s.StatLabel.Render(formatWPM(st.RawWPM)+" raw wpm"),
	))
	b.WriteString("\n\n")

	b.WriteString(r.ctx.help("enter/r again", "esc menu"))
	return b.String()
}
