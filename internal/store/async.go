package store

import (
	"context"
	"errors"
	"sync"
	"time"
)

// writeTimeout bounds a single background write, so one wedged connection
// cannot stall the queue behind it forever.
const writeTimeout = 5 * time.Second

// ErrClosed is returned by a closed Async.
var ErrClosed = errors.New("store: repository is closed")

// Async wraps a Repository so that recording a run never blocks the caller.
//
// A run is finished the moment the typist reaches the end of the passage; the
// screen should change then, not once Postgres has acknowledged an insert
// across a proxy. Writes go onto a buffered channel drained by one goroutine,
// and a full buffer drops the run rather than blocking — the same trade the
// lobby makes when it broadcasts, for the same reason. Losing a row is a
// nuisance; stalling someone's race is a bug.
//
// Reads are not wrapped. They already run off the update loop, inside a
// tea.Cmd, where blocking costs nothing.
type Async struct {
	Repository // reads pass straight through

	runs    chan Run
	done    chan struct{}
	onError func(error)

	mu     sync.RWMutex
	closed bool
}

// NewAsync returns an Async writing through to repo, buffering up to buffer
// runs. onError, if non-nil, is called for dropped runs and failed writes; it
// may be called from the worker goroutine.
func NewAsync(repo Repository, buffer int, onError func(error)) *Async {
	a := &Async{
		Repository: repo,
		runs:       make(chan Run, buffer),
		done:       make(chan struct{}),
		onError:    onError,
	}
	go a.work()
	return a
}

// RecordRun queues a run. It does not report write failures — there is nobody
// to report them to by the time they happen — and reports drops through
// onError instead.
func (a *Async) RecordRun(_ context.Context, run Run) error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.closed {
		return ErrClosed
	}
	if run.CreatedAt.IsZero() {
		// Stamp it here rather than at write time: the run happened when the
		// typist finished, not when the queue got round to it.
		run.CreatedAt = time.Now()
	}

	select {
	case a.runs <- run:
		return nil
	default:
		a.report(errors.New("store: write queue full, run dropped"))
		return nil
	}
}

// Close stops accepting runs, waits for the queue to drain, and returns. It is
// safe to call more than once.
func (a *Async) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	close(a.runs)
	a.mu.Unlock()

	<-a.done
	return nil
}

func (a *Async) work() {
	defer close(a.done)

	for run := range a.runs {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		if err := a.Repository.RecordRun(ctx, run); err != nil {
			a.report(err)
		}
		cancel()
	}
}

func (a *Async) report(err error) {
	if a.onError != nil {
		a.onError(err)
	}
}
