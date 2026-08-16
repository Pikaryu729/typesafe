// Package economy decides what a finished attempt is worth in bytes.
//
// Bytes are the app's only currency and typing is their only source: there is
// no code path anywhere that turns money into them. What a race pays is a
// small random base plus deterministic bonuses, so a typist can learn the
// system from one breakdown and predict the next one.
//
// Like lobby, typing, words and store, this package has no Bubble Tea or
// terminal dependency. The randomness is injected rather than package-level,
// which is what makes an award assertable in a test — the same reason
// lobby.WithRand exists.
package economy

import "math/rand/v2"

// Unit is what the currency is called, in one place so every screen agrees.
const Unit = "bytes"

// The base payout is the only random part of an award, drawn uniformly from
// these ranges. Everything else is a deterministic bonus: a player who cannot
// work out why they earned what they did has no reason to chase accuracy.
const (
	raceBaseMin, raceBaseMax         = 8, 12
	practiceBaseMin, practiceBaseMax = 2, 4
)

// Placement pays for beating people, not merely for placing. The kicker
// rewards the podium and beatenEach pays per racer left behind, so first of
// five is worth well over first of two.
//
// That ordering matters because a lobby can be re-raced indefinitely through
// Rematch: a two-player rematch loop is the least rewarding way to earn bytes
// while still paying honestly for a real head-to-head.
const beatenEach = 3

// placeKicker is what the podium is worth on its own, before the racers
// beaten are counted. A switch rather than a table so nothing package-level is
// shared between the sessions calling this concurrently.
func placeKicker(place int) int {
	switch place {
	case 1:
		return 12
	case 2:
		return 6
	case 3:
		return 3
	default:
		return 0
	}
}

// Bonus tiers, richest first. A race pays roughly double what practice does
// for the same typing, which is what keeps racing the point.
var (
	raceAccuracyTiers = []tier{{0.99, 8}, {0.97, 5}, {0.94, 3}, {0.90, 1}}
	pracAccuracyTiers = []tier{{0.99, 4}, {0.97, 3}, {0.94, 2}, {0.90, 1}}
)

// Speed pays a byte per this many words per minute, up to a cap. The cap
// stops a single blistering run from being worth a whole evening of racing.
const (
	raceWPMPerByte, raceSpeedCap = 15, 10
	pracWPMPerByte, pracSpeedCap = 25, 4
)

// tier is one accuracy threshold and what reaching it pays.
type tier struct {
	atLeast float64
	bonus   int
}

// Line is one row of an award's breakdown.
type Line struct {
	Label  string
	Amount int
}

// Award is what an attempt earned and why.
//
// Lines are in display order and always sum to Total, which is what lets the
// results screen render the breakdown without doing any arithmetic of its
// own. A zero Award — no lines, no total — means the attempt earned nothing,
// and screens render nothing for it.
type Award struct {
	Lines []Line
	Total int
}

// Empty reports whether this award is worth nothing.
func (a Award) Empty() bool { return a.Total == 0 && len(a.Lines) == 0 }

// RaceInput is one racer's finish, as the lobby judged it.
type RaceInput struct {
	// Place is 1-based. Zero means they did not finish.
	Place int
	// Racers is how many players were in the standings, finishers and
	// non-finishers alike.
	Racers   int
	WPM      float64
	Accuracy float64 // in [0,1]
}

// AwardRace returns what a race finish earned.
//
// A racer who did not finish earns nothing at all, matching the existing rule
// that a half-typed passage is not recorded: their speed over a passage they
// abandoned is not a result they would want counted, and it should not be a
// way to farm bytes either.
func AwardRace(rng *rand.Rand, in RaceInput) Award {
	if in.Place <= 0 {
		return Award{}
	}

	var b builder
	b.add("base", between(rng, raceBaseMin, raceBaseMax))
	b.add(placeLabel(in.Place), placeBonus(in.Place, in.Racers))
	b.add("accuracy bonus", accuracyBonus(in.Accuracy, raceAccuracyTiers))
	b.add("speed bonus", speedBonus(in.WPM, raceWPMPerByte, raceSpeedCap))
	return b.award()
}

// AwardPractice returns what a solo attempt earned.
//
// Practice pays a trickle rather than nothing so that someone alone on the
// server still makes progress. It is deliberately a fraction of a race: the
// competition is the point, and this is the floor under an empty lobby.
func AwardPractice(rng *rand.Rand, wpm, accuracy float64) Award {
	var b builder
	b.add("base", between(rng, practiceBaseMin, practiceBaseMax))
	b.add("accuracy bonus", accuracyBonus(accuracy, pracAccuracyTiers))
	b.add("speed bonus", speedBonus(wpm, pracWPMPerByte, pracSpeedCap))
	return b.award()
}

// builder accumulates lines and keeps Total in step with them.
type builder struct{ a Award }

// add appends a line, dropping the ones worth nothing. An award reads better
// without a row of zeroes, and the first line — the base — is always positive.
func (b *builder) add(label string, amount int) {
	if amount <= 0 {
		return
	}
	b.a.Lines = append(b.a.Lines, Line{Label: label, Amount: amount})
	b.a.Total += amount
}

func (b *builder) award() Award { return b.a }

// placeLabel names the placement line. "win bonus" is worth spelling out;
// every other placing is just a placing.
func placeLabel(place int) string {
	if place == 1 {
		return "win bonus"
	}
	return "place bonus"
}

func placeBonus(place, racers int) int {
	return placeKicker(place) + beatenEach*max(0, racers-place)
}

func accuracyBonus(accuracy float64, tiers []tier) int {
	for _, t := range tiers {
		if accuracy >= t.atLeast {
			return t.bonus
		}
	}
	return 0
}

func speedBonus(wpm float64, perByte, limit int) int {
	if wpm <= 0 {
		return 0
	}
	return min(limit, int(wpm)/perByte)
}

// between returns a uniform int in [lo, hi].
func between(rng *rand.Rand, lo, hi int) int {
	return lo + rng.IntN(hi-lo+1)
}
