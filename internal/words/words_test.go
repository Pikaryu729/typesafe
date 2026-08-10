package words

import (
	"slices"
	"strings"
	"testing"
)

func TestEmbeddedListIsWellFormed(t *testing.T) {
	got := List()
	if len(got) == 0 {
		t.Fatal("word list is empty")
	}

	seen := make(map[string]bool, len(got))
	for i, w := range got {
		if w == "" {
			t.Fatalf("word %d is empty", i)
		}
		if strings.TrimSpace(w) != w {
			t.Errorf("word %d (%q) has surrounding whitespace", i, w)
		}
		if strings.ToLower(w) != w {
			t.Errorf("word %d (%q) is not lowercase", i, w)
		}
		if seen[w] {
			t.Errorf("word %d (%q) is a duplicate", i, w)
		}
		seen[w] = true
	}
}

func TestListReturnsCopy(t *testing.T) {
	first := List()
	original := first[0]
	first[0] = "clobbered"

	if got := List()[0]; got != original {
		t.Errorf("mutating the returned slice changed the package list: got %q, want %q", got, original)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	a := Generate(42, 25)
	b := Generate(42, 25)

	if !slices.Equal(a, b) {
		t.Errorf("same seed produced different passages:\n a = %v\n b = %v", a, b)
	}
}

func TestGenerateVariesBySeed(t *testing.T) {
	// With 1000 words and 25 draws, two different seeds colliding is
	// vanishingly unlikely, so an exact match means the seed is being ignored.
	if a, b := Generate(1, 25), Generate(2, 25); slices.Equal(a, b) {
		t.Errorf("different seeds produced identical passages: %v", a)
	}
}

func TestGenerateLength(t *testing.T) {
	for _, n := range []int{1, 10, 50, 250} {
		if got := len(Generate(7, n)); got != n {
			t.Errorf("Generate(7, %d) returned %d words, want %d", n, got, n)
		}
	}
}

func TestGenerateNonPositiveCount(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		if got := Generate(7, n); got != nil {
			t.Errorf("Generate(7, %d) = %v, want nil", n, got)
		}
	}
}

func TestGenerateDrawsFromList(t *testing.T) {
	valid := make(map[string]bool, Len())
	for _, w := range List() {
		valid[w] = true
	}

	for _, w := range Generate(99, 500) {
		if !valid[w] {
			t.Fatalf("generated word %q is not in the list", w)
		}
	}
}

func TestGenerateCoversTheList(t *testing.T) {
	// A large draw should touch most of the list. This catches a generator
	// stuck on a narrow slice of indices, which the determinism tests would
	// happily pass.
	seen := make(map[string]bool)
	for _, w := range Generate(5, 20000) {
		seen[w] = true
	}

	if min := Len() * 9 / 10; len(seen) < min {
		t.Errorf("20000 draws covered only %d of %d words, want at least %d", len(seen), Len(), min)
	}
}

func TestPassage(t *testing.T) {
	const n = 20
	got := Passage(3, n)

	if strings.TrimSpace(got) != got {
		t.Errorf("Passage(3, %d) = %q, has surrounding whitespace", n, got)
	}
	if strings.Contains(got, "  ") {
		t.Errorf("Passage(3, %d) = %q, contains a double space", n, got)
	}
	if fields := strings.Split(got, " "); len(fields) != n {
		t.Errorf("Passage(3, %d) split into %d words, want %d", n, len(fields), n)
	}
	if want := strings.Join(Generate(3, n), " "); got != want {
		t.Errorf("Passage(3, %d) = %q, want %q", n, got, want)
	}
}

func TestPassageEmpty(t *testing.T) {
	if got := Passage(3, 0); got != "" {
		t.Errorf("Passage(3, 0) = %q, want empty string", got)
	}
}

func TestNewSeedVaries(t *testing.T) {
	// Not a randomness test — just guards against a stubbed-out constant.
	seen := make(map[int64]bool)
	for range 20 {
		seen[NewSeed()] = true
	}
	if len(seen) == 1 {
		t.Error("NewSeed returned the same value 20 times")
	}
}
