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
	mu        sync.Mutex
	users     map[string]User    // id -> user
	keys      map[string]string  // fingerprint -> user id
	runs      map[string][]Run   // user id -> runs, newest first
	purchases map[string][]Owned // user id -> cosmetics owned, oldest first
	codes     map[string]memCode

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
		users:     make(map[string]User),
		keys:      make(map[string]string),
		runs:      make(map[string][]Run),
		purchases: make(map[string][]Owned),
		codes:     make(map[string]memCode),
		now:       time.Now,
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

func (m *Memory) Wallet(_ context.Context, userID string) (Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.walletLocked(userID), nil
}

// walletLocked builds a wallet from the two things it is derived from. Buy and
// Equip return one too, so this exists to keep them from re-taking the lock.
func (m *Memory) walletLocked(userID string) Wallet {
	return Wallet{
		Balance: Balance(m.runs[userID], m.purchases[userID]),
		Owned:   slices.Clone(m.purchases[userID]),
	}
}

func (m *Memory) Buy(_ context.Context, userID string, item Owned) (Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, o := range m.purchases[userID] {
		if o.ID == item.ID {
			return Wallet{}, ErrAlreadyOwned
		}
	}
	if Balance(m.runs[userID], m.purchases[userID]) < item.Price {
		return Wallet{}, ErrInsufficientFunds
	}

	if item.BoughtAt.IsZero() {
		item.BoughtAt = m.now()
	}
	// A purchase is never equipped by the act of buying it; the caller equips
	// it afterwards, through the one path that enforces one per slot.
	item.Equipped = false
	m.purchases[userID] = append(m.purchases[userID], item)
	return m.walletLocked(userID), nil
}

func (m *Memory) Equip(_ context.Context, userID, slot, cosmeticID string) (Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	owned := m.purchases[userID]
	if cosmeticID != "" && !slices.ContainsFunc(owned, func(o Owned) bool {
		return o.ID == cosmeticID && o.Slot == slot
	}) {
		return Wallet{}, ErrNotOwned
	}

	// Clear the slot first, then wear the one. Doing it in that order is what
	// makes an empty cosmeticID mean "back to the default" without a branch.
	for i := range owned {
		if owned[i].Slot == slot {
			owned[i].Equipped = owned[i].ID == cosmeticID
		}
	}
	return m.walletLocked(userID), nil
}

// mergePurchasesLocked moves the source account's cosmetics onto the target.
//
// Two rules, both of which the SQL repeats. A cosmetic the target already owns
// is dropped rather than moved, because owning one twice is not a thing; since
// a balance is earned less bought, dropping that row hands its price back —
// the duplicate is refunded, which is the only fair outcome. And everything
// that moves arrives unequipped, so two accounts wearing different colours
// cannot merge into one wearing both.
func (m *Memory) mergePurchasesLocked(targetID, sourceID string) {
	held := make(map[string]bool, len(m.purchases[targetID]))
	for _, o := range m.purchases[targetID] {
		held[o.ID] = true
	}

	for _, o := range m.purchases[sourceID] {
		if held[o.ID] {
			continue
		}
		o.Equipped = false
		m.purchases[targetID] = append(m.purchases[targetID], o)
	}
	delete(m.purchases, sourceID)
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
	m.mergePurchasesLocked(target.ID, source)
	for fp, id := range m.keys {
		if id == source {
			m.keys[fp] = target.ID
		}
	}
	delete(m.users, source)

	return target, nil
}
