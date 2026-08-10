package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Pikaryu729/typesafe/internal/lobby"
	"github.com/Pikaryu729/typesafe/internal/typing"
	"github.com/Pikaryu729/typesafe/internal/words"
)

const (
	// raceTick is how often the race screen redraws and reports progress.
	// Faster than solo practice so opponents' bars move smoothly; ten updates
	// a second per racer is small enough to fan out to a full lobby.
	raceTick = 100 * time.Millisecond
	// barWidth is the width of a progress bar in characters.
	barWidth = 24
)

// raceTickMsg drives the race screen's clock and progress reporting.
type raceTickMsg time.Time

func scheduleRaceTick() tea.Cmd {
	return tea.Tick(raceTick, func(t time.Time) tea.Msg { return raceTickMsg(t) })
}

// Race is the live racing screen.
type Race struct {
	ctx   *Context
	lobby *lobby.Lobby
	sess  *typing.Session
	snap  lobby.Snapshot

	// progress is each racer's latest position, keyed by player ID. It is fed
	// by ProgressUpdated events, which arrive far more often than snapshots.
	progress map[string]raceProgress

	// seed and wordCount describe the passage, kept so a finished race can be
	// stored as the numbers that reproduce it.
	seed      int64
	wordCount int

	// reported is the position last sent to the lobby, so an idle racer does
	// not generate traffic.
	reported int
	finished bool
}

type raceProgress struct {
	chars int
	wpm   float64
}

// NewRace starts racing from the lobby's shared start event.
//
// The passage is generated locally from the seed rather than received, and the
// attempt is timed from the shared instant rather than from this player's
// first keystroke.
func NewRace(ctx *Context, l *lobby.Lobby, ev lobby.RaceStarted) Race {
	return Race{
		ctx:       ctx,
		lobby:     l,
		sess:      typing.New(words.Passage(ev.Seed, ev.Words), typing.StartedAt(ev.StartAt)),
		snap:      l.Snapshot(),
		progress:  make(map[string]raceProgress),
		seed:      ev.Seed,
		wordCount: ev.Words,
	}
}

// Init does not subscribe: the waiting room already did, and the subscription
// carries across screens within the same lobby.
func (r Race) Init() tea.Cmd { return scheduleRaceTick() }

func (r Race) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case raceTickMsg:
		if r.finished {
			return r, scheduleRaceTick() // keep the clock live for opponents
		}
		r.report()
		return r, scheduleRaceTick()

	case lobby.ProgressUpdated:
		r.progress[msg.PlayerID] = raceProgress{chars: msg.CharsTyped, wpm: msg.WPM}
		return r, nil

	case lobby.LobbyUpdated:
		r.snap = msg.Snapshot
		return r, nil

	case lobby.PlayerFinished:
		r.progress[msg.PlayerID] = raceProgress{chars: r.sess.Len(), wpm: msg.Result.WPM}
		return r, nil

	case lobby.RaceEnded:
		r.recordOwnResult(msg.Results)
		return r, navigate(NewRaceResults(r.ctx, r.lobby, msg.Results))

	case tea.KeyMsg:
		return r.handleKey(msg)
	}
	return r, nil
}

// recordOwnResult stores this player's finish, and only this player's: each
// session writes its own row, so the lobby package never needs to know that
// persistence exists.
//
// A racer who did not reach the end is not recorded. Their speed over a
// half-typed passage is not a result they would want counted among their
// bests.
func (r Race) recordOwnResult(results []lobby.Result) {
	for _, res := range results {
		if res.PlayerID != r.ctx.PlayerID {
			continue
		}
		if !res.Finished {
			return
		}
		r.ctx.record(raceRun(r.seed, r.wordCount, r.lobby.Code(), res, r.sess.Stats()))
		return
	}
}

func (r Race) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		r.ctx.leaveLobby(r.lobby)
		return r, navigate(NewBrowser(r.ctx))
	}
	if r.finished {
		return r, nil // no more typing once you are over the line
	}

	switch msg.Type {
	case tea.KeyBackspace:
		r.sess.Backspace()
		return r, nil
	case tea.KeySpace:
		r.sess.Type(' ')
	case tea.KeyRunes:
		if msg.Alt {
			return r, nil
		}
		for _, ch := range msg.Runes {
			r.sess.Type(ch)
		}
	default:
		return r, nil
	}

	if r.sess.Finished() {
		r.finished = true
		r.lobby.Finish(r.ctx.PlayerID, r.sess.Pos(), r.sess.WPM(), r.sess.Accuracy())
	}
	return r, nil
}

// report publishes this racer's position, but only when it has moved.
func (r *Race) report() {
	if pos := r.sess.Pos(); pos != r.reported {
		r.reported = pos
		r.lobby.RecordProgress(r.ctx.PlayerID, pos, r.sess.WPM(), r.sess.Accuracy())
	}
}

func (r Race) View() string {
	st := r.sess.Stats()

	var out strings.Builder
	out.WriteString(r.ctx.header("race " + r.snap.Code))
	out.WriteString("\n\n")
	out.WriteString(r.renderRacers())
	out.WriteString("\n")
	out.WriteString(renderPassage(r.ctx.Styles, r.sess, r.ctx.contentWidth(), r.ctx.passageLines()))
	out.WriteString("\n\n")
	out.WriteString(r.ctx.statRow(
		r.ctx.stat(formatWPM(st.WPM), "wpm"),
		r.ctx.stat(formatAccuracy(st.Accuracy), "acc"),
		r.ctx.stat(formatDuration(st.Elapsed), "time"),
	))
	out.WriteString("\n\n")

	if r.finished {
		out.WriteString(r.ctx.Styles.Good.Render("finished — waiting for the others"))
	} else {
		out.WriteString(r.ctx.help("esc leave"))
	}
	return out.String()
}

func (r Race) renderRacers() string {
	s := r.ctx.Styles
	total := max(1, r.sess.Len())

	var out strings.Builder
	for _, p := range r.snap.Players {
		prog := r.progress[p.ID]
		// Our own bar comes from the local session, which is always ahead of
		// the round trip through the lobby.
		if p.ID == r.ctx.PlayerID {
			prog = raceProgress{chars: r.sess.Pos(), wpm: r.sess.WPM()}
		}

		style := s.Subtitle
		switch {
		case p.Finished:
			style = s.Good
		case p.ID == r.ctx.PlayerID:
			style = s.StatValue
		}

		name := p.Name
		if p.ID == r.ctx.PlayerID {
			name += " (you)"
		}

		out.WriteString(fmt.Sprintf("%-18s %s %3.0f wpm%s\n",
			truncate(name, 18),
			style.Render(renderBar(float64(prog.chars)/float64(total))),
			prog.wpm,
			placeSuffix(s, p)))
	}
	return out.String()
}

// placeSuffix marks a racer who has already crossed the line.
func placeSuffix(s Styles, p lobby.PlayerState) string {
	if !p.Finished {
		return ""
	}
	return "  " + s.Good.Render(ordinal(p.Place))
}

// renderBar draws a proportional bar, clamped to [0,1].
func renderBar(frac float64) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * barWidth)
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
}

// ordinal renders a finishing place as 1st, 2nd, 3rd and so on.
func ordinal(n int) string {
	if n <= 0 {
		return ""
	}
	suffix := "th"
	// 11th, 12th and 13th are the exceptions to the digit rule.
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}
