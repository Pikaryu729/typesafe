package economy_test

import (
	"math/rand/v2"
	"testing"

	"github.com/Pikaryu729/typesafe/internal/economy"
)

// testRand returns a deterministic source, so an award is the same on every
// run and can be asserted exactly.
func testRand() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

// lineAmount returns the amount of the named line, and whether it was present.
func lineAmount(a economy.Award, label string) (int, bool) {
	for _, l := range a.Lines {
		if l.Label == label {
			return l.Amount, true
		}
	}
	return 0, false
}

func TestRaceAwardBreaksDownAndSumsToTotal(t *testing.T) {
	a := economy.AwardRace(testRand(), economy.RaceInput{
		Place: 1, Racers: 2, WPM: 65, Accuracy: 0.96,
	})

	var sum int
	for _, l := range a.Lines {
		if l.Amount <= 0 {
			t.Errorf("line %q is worth %d; zero lines should be dropped", l.Label, l.Amount)
		}
		sum += l.Amount
	}
	if sum != a.Total {
		t.Errorf("lines sum to %d but Total is %d", sum, a.Total)
	}

	for _, want := range []string{"base", "win bonus", "accuracy bonus", "speed bonus"} {
		if _, ok := lineAmount(a, want); !ok {
			t.Errorf("award is missing the %q line: %+v", want, a.Lines)
		}
	}
}

func TestRaceAwardLineValues(t *testing.T) {
	// 1st of 2 pays the win kicker of 12 plus 3 for the one racer beaten;
	// 96% clears the 94% tier; 65 wpm is four whole multiples of 15.
	a := economy.AwardRace(testRand(), economy.RaceInput{
		Place: 1, Racers: 2, WPM: 65, Accuracy: 0.96,
	})

	for _, tc := range []struct {
		label string
		want  int
	}{
		{"win bonus", 15},
		{"accuracy bonus", 3},
		{"speed bonus", 4},
	} {
		got, ok := lineAmount(a, tc.label)
		if !ok {
			t.Fatalf("no %q line in %+v", tc.label, a.Lines)
		}
		if got != tc.want {
			t.Errorf("%s = %d, want %d", tc.label, got, tc.want)
		}
	}
}

func TestPlacementCountsOnlyFinishedRacers(t *testing.T) {
	one := economy.AwardRace(testRand(), economy.RaceInput{
		Place: 1, Racers: 1, WPM: 60, Accuracy: 0.95,
	})
	duel := economy.AwardRace(testRand(), economy.RaceInput{
		Place: 1, Racers: 2, WPM: 60, Accuracy: 0.95,
	})

	oneBonus, _ := lineAmount(one, "win bonus")
	duelBonus, _ := lineAmount(duel, "win bonus")
	if oneBonus != 12 || duelBonus != 15 {
		t.Errorf("win bonuses = %d and %d, want 12 and 15", oneBonus, duelBonus)
	}
}

func TestBeatingMorePeoplePaysMore(t *testing.T) {
	small := economy.AwardRace(testRand(), economy.RaceInput{Place: 1, Racers: 2, WPM: 60, Accuracy: 0.95})
	big := economy.AwardRace(testRand(), economy.RaceInput{Place: 1, Racers: 5, WPM: 60, Accuracy: 0.95})

	if big.Total <= small.Total {
		t.Errorf("first of five paid %d, first of two paid %d; a bigger field must pay more",
			big.Total, small.Total)
	}
}

func TestWinningPaysMoreThanPlacing(t *testing.T) {
	first := economy.AwardRace(testRand(), economy.RaceInput{Place: 1, Racers: 4, WPM: 60, Accuracy: 0.95})
	third := economy.AwardRace(testRand(), economy.RaceInput{Place: 3, Racers: 4, WPM: 60, Accuracy: 0.95})

	if first.Total <= third.Total {
		t.Errorf("1st paid %d and 3rd paid %d; winning must pay more", first.Total, third.Total)
	}
}

func TestDidNotFinishEarnsNothing(t *testing.T) {
	a := economy.AwardRace(testRand(), economy.RaceInput{
		Place: 0, Racers: 3, WPM: 120, Accuracy: 1,
	})

	if !a.Empty() {
		t.Errorf("an unfinished race paid %+v; it must pay nothing", a)
	}
}

func TestAccuracyAndSpeedMoveTheAward(t *testing.T) {
	in := economy.RaceInput{Place: 2, Racers: 3, WPM: 40, Accuracy: 0.80}

	sloppy := economy.AwardRace(testRand(), in)

	accurate := in
	accurate.Accuracy = 0.99
	fast := in
	fast.WPM = 120

	if got := economy.AwardRace(testRand(), accurate); got.Total <= sloppy.Total {
		t.Errorf("99%% accuracy paid %d, 80%% paid %d", got.Total, sloppy.Total)
	}
	if got := economy.AwardRace(testRand(), fast); got.Total <= sloppy.Total {
		t.Errorf("120 wpm paid %d, 40 wpm paid %d", got.Total, sloppy.Total)
	}
}

func TestSpeedBonusIsCapped(t *testing.T) {
	quick := economy.AwardRace(testRand(), economy.RaceInput{Place: 1, Racers: 2, WPM: 150, Accuracy: 0.95})
	absurd := economy.AwardRace(testRand(), economy.RaceInput{Place: 1, Racers: 2, WPM: 10000, Accuracy: 0.95})

	if quick.Total != absurd.Total {
		t.Errorf("150 wpm paid %d and 10000 wpm paid %d; the speed bonus should be capped",
			quick.Total, absurd.Total)
	}
}

func TestPracticePaysATrickle(t *testing.T) {
	rng := testRand()
	for range 200 {
		a := economy.AwardPractice(rng, 70, 0.95)
		if a.Total <= 0 {
			t.Fatalf("practice paid nothing: %+v", a)
		}
		if a.Total > 12 {
			t.Fatalf("practice paid %d, which is race money", a.Total)
		}
		if _, ok := lineAmount(a, "win bonus"); ok {
			t.Fatal("a solo attempt was paid a win bonus")
		}
	}
}

func TestRaceOutEarnsPracticeForTheSameTyping(t *testing.T) {
	rng := testRand()

	// Over many rolls the worst race must still beat the best practice, so
	// the randomness never makes practising the better choice.
	worstRace, bestPractice := 1<<30, 0
	for range 500 {
		if got := economy.AwardRace(rng, economy.RaceInput{Place: 2, Racers: 2, WPM: 70, Accuracy: 0.95}).Total; got < worstRace {
			worstRace = got
		}
		if got := economy.AwardPractice(rng, 70, 0.95).Total; got > bestPractice {
			bestPractice = got
		}
	}
	if worstRace <= bestPractice {
		t.Errorf("worst race paid %d, best practice paid %d", worstRace, bestPractice)
	}
}

func TestBaseStaysInRange(t *testing.T) {
	rng := testRand()
	for range 500 {
		got, ok := lineAmount(economy.AwardRace(rng, economy.RaceInput{
			Place: 1, Racers: 2, WPM: 60, Accuracy: 0.95,
		}), "base")
		if !ok {
			t.Fatal("no base line")
		}
		if got < 8 || got > 12 {
			t.Fatalf("base = %d, outside the documented [8,12]", got)
		}
	}
}

func TestBaseIsActuallyRandom(t *testing.T) {
	rng := testRand()
	seen := make(map[int]bool)
	for range 200 {
		got, _ := lineAmount(economy.AwardRace(rng, economy.RaceInput{
			Place: 1, Racers: 2, WPM: 60, Accuracy: 0.95,
		}), "base")
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Errorf("the base was %v every time; it is meant to be random", seen)
	}
}
