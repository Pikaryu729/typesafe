package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/typing"
	"github.com/Pikaryu729/typesafe/internal/words"
)

const (
	// practiceWords is the length of a solo passage.
	practiceWords = 30
	// tickInterval is how often the live stats refresh. Elapsed time and WPM
	// change on their own, so the screen cannot wait for a keystroke to
	// redraw. Four times a second reads as live without busy work.
	tickInterval = 250 * time.Millisecond
)

// tickMsg drives the live stats clock.
type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Practice is the solo typing screen.
type Practice struct {
	ctx  *Context
	sess *typing.Session
	// seed is kept so a finished run can be stored as the two numbers that
	// reproduce its passage, rather than as the text.
	seed int64
}

// NewPractice starts an attempt at a freshly generated passage.
func NewPractice(ctx *Context) Practice {
	seed := words.NewSeed()
	return Practice{
		ctx:  ctx,
		sess: typing.New(words.Passage(seed, practiceWords)),
		seed: seed,
	}
}

func (p Practice) Init() tea.Cmd { return tick() }

func (p Practice) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		// Stop the clock once there is nothing left to update.
		if p.sess.Finished() {
			return p, nil
		}
		return p, tick()

	case tea.KeyMsg:
		return p.handleKey(msg)
	}
	return p, nil
}

func (p Practice) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	if p.sess.Finished() {
		return p, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		return p, navigate(NewMenu(p.ctx))

	case tea.KeyTab, tea.KeyCtrlR:
		return NewPractice(p.ctx), tick() // a different passage, not a replay

	case tea.KeyBackspace:
		p.sess.Backspace()
		return p, nil

	case tea.KeySpace:
		p.sess.Type(' ')

	case tea.KeyRunes:
		if msg.Alt {
			return p, nil // alt-combinations are shortcuts, not text
		}
		for _, r := range msg.Runes {
			p.sess.Type(r)
		}

	default:
		return p, nil
	}

	if p.sess.Finished() {
		st := p.sess.Stats()
		award := p.ctx.awardPractice(st)

		run := practiceRun(p.seed, practiceWords, st)
		run.Earned = award.Total
		p.ctx.record(run)
		return p, navigate(newResults(p.ctx, st, award))
	}
	return p, nil
}

func (p Practice) View() string {
	st := p.sess.Stats()

	var b strings.Builder
	b.WriteString(p.ctx.header("practice"))
	b.WriteString("\n\n")
	b.WriteString(renderPassage(p.ctx.Styles, p.sess, p.ctx.contentWidth(), p.ctx.passageLines()))
	b.WriteString("\n\n")
	b.WriteString(p.ctx.statRow(
		p.ctx.stat(formatWPM(st.WPM), "wpm"),
		p.ctx.stat(formatAccuracy(st.Accuracy), "acc"),
		p.ctx.stat(formatDuration(st.Elapsed), "time"),
	))
	b.WriteString("\n\n")
	b.WriteString(p.ctx.help("type to begin", "tab new passage", "esc menu"))
	return b.String()
}

// Shared number formatting, so live stats and the results screen agree.

func formatWPM(wpm float64) string { return fmt.Sprintf("%.0f", wpm) }

func formatAccuracy(acc float64) string { return fmt.Sprintf("%.0f%%", acc*100) }

func formatDuration(d time.Duration) string { return fmt.Sprintf("%.1fs", d.Seconds()) }
