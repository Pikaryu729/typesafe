package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/store"
)

const (
	// profileHistory is how many runs are fetched: enough for a sparkline with
	// some shape to it.
	profileHistory = 20
	// profileRows is how many of them are listed underneath.
	profileRows = 8
)

// sparkBlocks are the eight heights a sparkline column can take.
var sparkBlocks = []rune("▁▂▃▄▅▆▇█")

// Profile shows what the account has done: bests, totals, and recent runs.
type Profile struct {
	ctx *Context

	loading bool
	err     error
	summary store.Summary
	runs    []store.Run
}

// profileLoadedMsg carries the result of the history query back into the
// update loop.
type profileLoadedMsg struct {
	summary store.Summary
	runs    []store.Run
	err     error
}

// NewProfile returns the profile screen. It starts empty; Init fetches.
func NewProfile(ctx *Context) Profile {
	return Profile{ctx: ctx, loading: ctx.tracking()}
}

func (p Profile) Init() tea.Cmd {
	if !p.ctx.tracking() {
		return nil
	}
	return p.load()
}

// load queries the repository.
//
// It runs as a tea.Cmd, which Bubble Tea executes on its own goroutine, so a
// slow database delays this screen filling in and nothing else. That is the
// whole reason reads are not wrapped the way writes are.
func (p Profile) load() tea.Cmd {
	repo, userID := p.ctx.Repo, p.ctx.User.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()

		if err := store.Flush(ctx, repo); err != nil {
			return profileLoadedMsg{err: err}
		}

		summary, err := repo.Summary(ctx, userID)
		if err != nil {
			return profileLoadedMsg{err: err}
		}
		runs, err := repo.RecentRuns(ctx, userID, profileHistory)
		if err != nil {
			return profileLoadedMsg{err: err}
		}
		return profileLoadedMsg{summary: summary, runs: runs}
	}
}

func (p Profile) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case profileLoadedMsg:
		p.loading = false
		p.summary, p.runs, p.err = msg.summary, msg.runs, msg.err
		return p, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			return p, navigate(NewMenu(p.ctx))
		case "r":
			if p.ctx.tracking() {
				p.loading, p.err = true, nil
				return p, p.load()
			}
		}
	}
	return p, nil
}

func (p Profile) View() string {
	s := p.ctx.Styles

	var b strings.Builder
	b.WriteString(p.ctx.header(p.ctx.User.DisplayName))
	b.WriteString("\n\n")

	switch {
	case !p.ctx.tracking():
		b.WriteString(s.Subtitle.Render(p.unavailableReason()))
		b.WriteString("\n\n")
		b.WriteString(p.ctx.help("esc menu"))
		return b.String()

	case p.loading:
		b.WriteString(s.Subtitle.Render("loading…"))
		b.WriteString("\n\n")
		b.WriteString(p.ctx.help("esc menu"))
		return b.String()

	case p.err != nil:
		b.WriteString(s.Error.Render("could not load your history"))
		b.WriteString("\n")
		b.WriteString(s.Subtitle.Render("the database is not answering; your typing is unaffected"))
		b.WriteString("\n\n")
		b.WriteString(p.ctx.help("r retry", "esc menu"))
		return b.String()

	case p.summary.Runs == 0:
		b.WriteString(s.Subtitle.Render("nothing here yet — finish a passage and it will show up"))
		b.WriteString("\n\n")
		b.WriteString(p.ctx.help("esc menu"))
		return b.String()
	}

	b.WriteString(p.ctx.statRow(
		p.ctx.stat(formatWPM(p.summary.BestWPM), "best wpm"),
		p.ctx.stat(formatAccuracy(p.summary.BestAccuracy), "at"),
		p.ctx.stat(formatWPM(p.summary.RecentWPM), "recent avg"),
	))
	b.WriteString("\n\n")

	b.WriteString(p.ctx.statRow(
		s.StatLabel.Render(fmt.Sprintf("%d runs", p.summary.Runs)),
		s.StatLabel.Render(fmt.Sprintf("%d races", p.summary.Races)),
		s.Good.Render(fmt.Sprintf("%d won", p.summary.Wins)),
		s.StatLabel.Render(formatTotalTime(p.summary.TotalTime)+" typing"),
	))
	b.WriteString("\n\n")

	if line := sparkline(p.runs); line != "" {
		b.WriteString(s.StatValue.Render(line))
		b.WriteString("\n")
		b.WriteString(s.Help.Render("oldest → newest"))
		b.WriteString("\n\n")
	}

	b.WriteString(p.renderRuns())
	b.WriteString("\n")
	b.WriteString(p.ctx.help("r refresh", "esc menu"))
	return b.String()
}

// unavailableReason says which of the two ways this session has no history,
// because the fix is different for each.
func (p Profile) unavailableReason() string {
	if p.ctx.Repo == nil {
		return "this server is running without a database, so nothing is recorded"
	}
	return "your account could not be loaded, so this session is anonymous"
}

func (p Profile) renderRuns() string {
	s := p.ctx.Styles

	runs := p.runs
	if len(runs) > profileRows {
		runs = runs[:profileRows]
	}

	var b strings.Builder
	for _, r := range runs {
		kind := "practice"
		if r.Mode == store.ModeRace {
			kind = fmt.Sprintf("race %s", r.RaceCode)
			if r.Place > 0 {
				kind += fmt.Sprintf(" (%s)", ordinal(r.Place))
			}
		}
		b.WriteString(fmt.Sprintf("%s  %s  %s  %s\n",
			s.StatValue.Render(fmt.Sprintf("%4s", formatWPM(r.WPM))),
			s.StatLabel.Render(fmt.Sprintf("%4s", formatAccuracy(r.Accuracy))),
			s.Item.Render(fmt.Sprintf("%-16s", kind)),
			s.Help.Render(formatAgo(time.Since(r.CreatedAt))),
		))
	}
	return b.String()
}

// sparkline draws WPM over time, oldest on the left.
//
// Runs arrive newest first, which is the order the list wants and the reverse
// of the order a chart wants.
func sparkline(runs []store.Run) string {
	if len(runs) < 2 {
		return "" // a single column is not a trend
	}

	lo, hi := runs[0].WPM, runs[0].WPM
	for _, r := range runs {
		lo = min(lo, r.WPM)
		hi = max(hi, r.WPM)
	}

	var b strings.Builder
	for i := len(runs) - 1; i >= 0; i-- {
		// A flat history has no range to scale against; draw it mid-height
		// rather than dividing by zero.
		idx := len(sparkBlocks) / 2
		if hi > lo {
			idx = int((runs[i].WPM - lo) / (hi - lo) * float64(len(sparkBlocks)-1))
		}
		b.WriteRune(sparkBlocks[idx])
	}
	return b.String()
}

// formatTotalTime renders a lifetime total, where seconds stop being useful.
func formatTotalTime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// formatAgo renders how long ago something happened, coarsely.
func formatAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours())/24)
	}
}
