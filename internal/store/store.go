// Package store persists accounts and finished typing runs.
//
// Identity comes from the client's SSH public key rather than a password: the
// server already authenticated the key, so its fingerprint is a stable handle
// on a person at no cost to them. Several keys can point at one account, which
// is what makes a laptop and a desktop the same typist.
//
// Like lobby, typing and words, this package has no Bubble Tea or terminal
// dependency, so it is testable without a PTY. Implementations must be safe for
// concurrent use: every SSH session reaches the same one.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// Errors returned by RedeemLinkCode. They are distinguished because the screen
// says something different for each: an expired code should be reissued, an
// unknown one was mistyped.
var (
	ErrUnknownCode = errors.New("store: no such link code")
	ErrExpiredCode = errors.New("store: link code has expired")
)

// Errors returned by Buy and Equip. All three are ordinary outcomes the shop
// reports rather than failures, so they are values a screen can compare
// against and turn into a sentence.
var (
	ErrInsufficientFunds = errors.New("store: not enough bytes")
	ErrAlreadyOwned      = errors.New("store: already owned")
	ErrNotOwned          = errors.New("store: not owned")
)

// Mode is how a run was produced.
type Mode string

const (
	// ModePractice is a solo attempt, timed from the first keystroke.
	ModePractice Mode = "practice"
	// ModeRace is an attempt against other players, timed from a shared start.
	ModeRace Mode = "race"
)

// User is one typist. The display name is what other players see; it is not
// identity, and two accounts may share one.
type User struct {
	ID          string
	DisplayName string
	CreatedAt   time.Time
	LastSeenAt  time.Time
}

// Run is one finished attempt at a passage.
//
// The passage itself is not stored. Seed and WordCount reproduce it exactly
// through words.Passage, which keeps a run a few dozen bytes and leaves the
// door open to replaying one keystroke for keystroke later.
type Run struct {
	UserID     string
	Mode       Mode
	Seed       int64
	WordCount  int
	WPM        float64
	RawWPM     float64
	Accuracy   float64
	Duration   time.Duration
	Keystrokes int
	Correct    int
	Incorrect  int
	// RaceCode and Place are set for ModeRace only; Place is 1-based.
	RaceCode string
	Place    int
	// Earned is the currency this attempt paid out.
	//
	// It rides along in the run's own row rather than in a ledger of its own,
	// which is what keeps earning off the typing path: the write that records
	// the run is the write that records its bytes, so the two can never
	// disagree and there is no second round trip when a race ends.
	Earned int
	// CreatedAt is set by the repository when zero.
	CreatedAt time.Time
}

// Owned is one cosmetic a typist has bought.
//
// The price is what they actually paid, not what the catalogue asks today.
// Balances are derived from these rows, so a repricing that reached backwards
// would silently rewrite everyone's balance.
type Owned struct {
	ID       string
	Slot     string
	Price    int
	Equipped bool
	BoughtAt time.Time
}

// Wallet is everything the shop needs about one typist.
type Wallet struct {
	Balance int
	Owned   []Owned
}

// EquippedIn returns the cosmetic worn in a slot, or "" for the default.
func (w Wallet) EquippedIn(slot string) string {
	for _, o := range w.Owned {
		if o.Slot == slot && o.Equipped {
			return o.ID
		}
	}
	return ""
}

// Owns reports whether this typist has bought a cosmetic.
func (w Wallet) Owns(id string) bool {
	for _, o := range w.Owned {
		if o.ID == id {
			return true
		}
	}
	return false
}

// Summary aggregates a user's history for the profile screen.
type Summary struct {
	Runs         int
	Races        int
	Wins         int           // races finished in first place
	BestWPM      float64       // over practice and races alike
	BestAccuracy float64       // accuracy of the best-WPM run, not the best ever
	RecentWPM    float64       // mean over the last RecentWindow runs
	TotalTime    time.Duration // time spent typing, not time connected
}

// RecentWindow is how many runs Summary.RecentWPM averages over. Small enough
// to move when you improve, large enough not to swing on one bad passage.
const RecentWindow = 10

// LinkCode lets a second machine join an existing account. It is short-lived
// and single-use: anyone who sees the code can claim the account, so it is
// closer to a password than to a lobby code.
type LinkCode struct {
	Code      string
	ExpiresAt time.Time
}

// LinkCodeTTL bounds how long a link code stays valid.
const LinkCodeTTL = 10 * time.Minute

// Repository is the persistence the app needs. A nil Repository is a valid
// state meaning "no database": the caller runs anonymously rather than failing.
type Repository interface {
	// ResolveUser returns the account owning fingerprint, creating one named
	// connectedName if the key has not been seen before. It also refreshes the
	// account's last-seen time.
	ResolveUser(ctx context.Context, fingerprint, connectedName string) (User, error)

	// RecordRun stores a finished attempt.
	RecordRun(ctx context.Context, run Run) error

	// RecentRuns returns a user's runs, newest first.
	RecentRuns(ctx context.Context, userID string, limit int) ([]Run, error)

	// Summary aggregates a user's history. A user with no runs yields a zero
	// Summary rather than an error.
	Summary(ctx context.Context, userID string) (Summary, error)

	// Wallet returns a user's balance and the cosmetics they own. A user who
	// has never earned or bought anything yields a zero Wallet, not an error.
	Wallet(ctx context.Context, userID string) (Wallet, error)

	// Buy records a purchase and returns the wallet it left behind.
	//
	// It fails with ErrInsufficientFunds when the balance will not cover the
	// price and ErrAlreadyOwned when the cosmetic is already theirs, and it
	// must be atomic against both: two sessions of the same account spending
	// the same bytes at the same instant is a real case, since one person can
	// hold several connections.
	//
	// The price comes from the caller rather than from a catalogue this
	// package knows. That is safe here and nowhere else: there is no client to
	// distrust, because the terminal UI runs in this same server process.
	Buy(ctx context.Context, userID string, item Owned) (Wallet, error)

	// Equip wears one owned cosmetic in a slot, replacing whatever was there.
	// An empty cosmeticID takes the slot back to the default. Equipping
	// something the user does not own fails with ErrNotOwned.
	Equip(ctx context.Context, userID, slot, cosmeticID string) (Wallet, error)

	// CreateLinkCode issues a code another session can redeem to join userID.
	CreateLinkCode(ctx context.Context, userID string) (LinkCode, error)

	// RedeemLinkCode attaches fingerprint to the account that issued code and
	// returns it.
	//
	// The key is already attached to an account — every session resolves one at
	// login — so redeeming merges that account into the target: its runs and
	// any other keys move across and it is deleted. That is what a typist
	// means by "this is also me". Redeeming a code issued by your own account
	// is a no-op.
	RedeemLinkCode(ctx context.Context, code, fingerprint string) (User, error)
}

// Summarize aggregates runs, newest first, into a Summary.
//
// It is the definition of what these figures mean. Memory calls it directly;
// the Postgres repository computes the same thing in SQL and the integration
// tests check the two agree, so the semantics live in one place even though
// there are two implementations.
func Summarize(runs []Run) Summary {
	var s Summary
	for i, r := range runs {
		s.Runs++
		s.TotalTime += r.Duration
		if r.Mode == ModeRace {
			s.Races++
			if r.Place == 1 {
				s.Wins++
			}
		}
		if r.WPM > s.BestWPM {
			s.BestWPM = r.WPM
			// Paired with the best run rather than the best ever seen: the
			// accuracy that produced your fastest time is the interesting
			// number, and a perfect score on a crawl is not a personal best.
			s.BestAccuracy = r.Accuracy
		}
		if i < RecentWindow {
			s.RecentWPM += r.WPM
		}
	}
	if n := min(len(runs), RecentWindow); n > 0 {
		s.RecentWPM /= float64(n)
	}
	return s
}

// Balance is the definition of what a balance means: everything a typist has
// earned, less everything they have bought.
//
// It is derived rather than stored, for the same reason Summarize is. A total
// kept in its own column is a second copy of the truth, and the two drift the
// first time a write half-succeeds; this cannot, because there is only one
// copy. Memory calls this directly, the Postgres repository recomputes it in
// SQL, and the integration tests check the two agree.
//
// runs may be in any order — a sum does not care — which is the one way this
// differs from Summarize.
func Balance(runs []Run, owned []Owned) int {
	var b int
	for _, r := range runs {
		b += r.Earned
	}
	for _, o := range owned {
		b -= o.Price
	}
	return b
}

// linkCodeAlphabet omits characters that are misread when spoken or typed:
// no O/0, I/1/l, S/5, Z/2.
const linkCodeAlphabet = "ABCDEFGHJKLMNPQRTUVWXY346789"

// LinkCodeLength is six rather than the lobby's four. A lobby code is a
// convenience among people who can see each other; a link code hands over an
// account, so guessing one should not be cheap. It is exported so the prompt
// that accepts one knows when to stop.
const LinkCodeLength = 6

// NewLinkCode returns a random code from the unambiguous alphabet. It is
// exported so every Repository issues codes in one format.
func NewLinkCode() string {
	b := make([]byte, LinkCodeLength)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail in practice. Returning a fixed string would
		// be worse than a predictable one only if it were also valid, and the
		// caller checks the error path that produced it.
		return ""
	}
	for i := range b {
		b[i] = linkCodeAlphabet[int(b[i])%len(linkCodeAlphabet)]
	}
	return string(b)
}

// NewID returns an opaque identifier for an account. Generated in Go rather
// than by the database so both implementations produce the same shape.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
