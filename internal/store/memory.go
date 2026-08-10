package store

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Memory is an in-process Repository.
//
// It exists for tests — every UI test needs a repository and none of them
// should need Postgres — and it doubles as the reference for what the SQL is
// supposed to do.
type Memory struct {
	mu    sync.Mutex
	users map[string]User   // id -> user
	keys  map[string]string // fingerprint -> user id
	runs  map[string][]Run  // user id -> runs, newest first
	codes map[string]memCode

	now func() time.Time
}

type memCode struct {
	userID    string
	expiresAt time.Time
}

// MemoryOption configures a Memory.
type MemoryOption func(*Memory)

// WithMemoryClock replaces the time source, so tests can expire link codes
// without sleeping.
func WithMemoryClock(now func() time.Time) MemoryOption {
	return func(m *Memory) { m.now = now }
}

// NewMemory returns an empty in-memory repository.
func NewMemory(opts ...MemoryOption) *Memory {
	m := &Memory{
		users: make(map[string]User),
		keys:  make(map[string]string),
		runs:  make(map[string][]Run),
		codes: make(map[string]memCode),
		now:   time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *Memory) ResolveUser(_ context.Context, fingerprint, connectedName string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if id, ok := m.keys[fingerprint]; ok {
		u := m.users[id]
		u.LastSeenAt = now
		m.users[id] = u
		return u, nil
	}

	u := User{
		ID:          NewID(),
		DisplayName: connectedName,
		CreatedAt:   now,
		LastSeenAt:  now,
	}
	m.users[u.ID] = u
	m.keys[fingerprint] = u.ID
	return u, nil
}

func (m *Memory) RecordRun(_ context.Context, run Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if run.CreatedAt.IsZero() {
		run.CreatedAt = m.now()
	}
	// Newest first, matching the order RecentRuns and Summarize expect.
	m.runs[run.UserID] = append([]Run{run}, m.runs[run.UserID]...)
	return nil
}

func (m *Memory) RecentRuns(_ context.Context, userID string, limit int) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	runs := m.runs[userID]
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	// Copy: the caller must not be able to reach into live state, the same rule
	// lobby snapshots follow.
	return slices.Clone(runs), nil
}

func (m *Memory) Summary(_ context.Context, userID string) (Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return Summarize(m.runs[userID]), nil
}

func (m *Memory) CreateLinkCode(_ context.Context, userID string) (LinkCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	code := NewLinkCode()
	expires := m.now().Add(LinkCodeTTL)
	m.codes[code] = memCode{userID: userID, expiresAt: expires}
	return LinkCode{Code: code, ExpiresAt: expires}, nil
}

func (m *Memory) RedeemLinkCode(_ context.Context, code, fingerprint string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.codes[code]
	if !ok {
		return User{}, ErrUnknownCode
	}
	if m.now().After(c.expiresAt) {
		delete(m.codes, code)
		return User{}, ErrExpiredCode
	}
	delete(m.codes, code) // single use, redeemed or not

	target, ok := m.users[c.userID]
	if !ok {
		return User{}, ErrUnknownCode // the account was merged away underneath us
	}

	source := m.keys[fingerprint]
	if source == "" || source == target.ID {
		// Either the key is new to us or it already belongs to the target;
		// pointing it at the target is all that is needed.
		m.keys[fingerprint] = target.ID
		return target, nil
	}

	// Fold the source account into the target: its runs and every key that
	// reached it, then drop the empty shell.
	m.runs[target.ID] = append(m.runs[target.ID], m.runs[source]...)
	slices.SortFunc(m.runs[target.ID], func(a, b Run) int { return b.CreatedAt.Compare(a.CreatedAt) })
	delete(m.runs, source)
	for fp, id := range m.keys {
		if id == source {
			m.keys[fp] = target.ID
		}
	}
	delete(m.users, source)

	return target, nil
}
