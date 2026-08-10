package ui

import (
	"context"
	"time"

	"github.com/Pikaryu729/typesafe/internal/lobby"
	"github.com/Pikaryu729/typesafe/internal/store"
	"github.com/Pikaryu729/typesafe/internal/typing"
)

// queryTimeout bounds a profile read. Long enough for a round trip through the
// Cloud SQL proxy, short enough that a wedged database shows an error instead
// of a screen that never fills in.
const queryTimeout = 5 * time.Second

// tracking reports whether this session's runs go anywhere.
//
// Two ways they do not: there is no database at all, or the account lookup
// failed and the session is anonymous. Screens check this rather than Repo
// alone, because an anonymous session has nothing to attach a run to.
func (c *Context) tracking() bool { return c.Repo != nil && c.User.ID != "" }

// record stores a finished attempt, and does nothing at all when the session
// is not tracked.
//
// It must not block: this runs inside the Bubble Tea update loop, where the
// screen change that follows a finished passage is waiting on it. In the
// server the repository is wrapped in store.Async, which makes the write a
// non-blocking send; tests pass a store.Memory, where it is a map insert.
func (c *Context) record(run store.Run) {
	if !c.tracking() {
		return
	}
	run.UserID = c.User.ID
	//nolint:errcheck // there is no one to report this to mid-race; Async logs.
	_ = c.Repo.RecordRun(context.Background(), run)
}

// practiceRun builds a Run from a finished solo attempt.
func practiceRun(seed int64, wordCount int, st typing.Stats) store.Run {
	return store.Run{
		Mode:       store.ModePractice,
		Seed:       seed,
		WordCount:  wordCount,
		WPM:        st.WPM,
		RawWPM:     st.RawWPM,
		Accuracy:   st.Accuracy,
		Duration:   st.Elapsed,
		Keystrokes: st.Keystrokes,
		Correct:    st.Correct,
		Incorrect:  st.Incorrect,
	}
}

// raceRun builds a Run from a finished race, given this player's own result.
//
// The figures come from both sides on purpose. Place, speed, accuracy and
// elapsed time are the lobby's, because a race is judged server-side against
// one clock for everyone; the keystroke counts are local, because only this
// session saw them.
func raceRun(seed int64, wordCount int, code string, res lobby.Result, st typing.Stats) store.Run {
	r := practiceRun(seed, wordCount, st)
	r.Mode = store.ModeRace
	r.RaceCode = code
	r.Place = res.Place
	r.WPM = res.WPM
	r.Accuracy = res.Accuracy
	r.Duration = res.Elapsed
	return r
}
