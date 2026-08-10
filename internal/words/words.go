// Package words provides the source text for typing practice and races.
//
// Passages are generated from a fixed list of common English words embedded in
// the binary. Generation is seeded and deterministic: a given seed always
// yields the same passage. That property is what lets every racer in a lobby
// render identical text from a shared seed, so the server never has to
// broadcast the passage itself.
package words

import (
	_ "embed"
	"math/rand/v2"
	"slices"
	"strings"
)

//go:embed english1k.txt
var embedded string

// list is the parsed word list. It is built once at init and treated as
// read-only afterwards, so concurrent readers need no synchronization.
var list = parseList(embedded)

// streamConst seeds the second half of the PCG state. Any fixed value works;
// this is the golden-ratio constant commonly used for the purpose.
const streamConst = 0x9E3779B97F4A7C15

func parseList(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if w := strings.TrimSpace(line); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// List returns a copy of the word list.
func List() []string {
	return slices.Clone(list)
}

// Len reports how many words are available.
func Len() int {
	return len(list)
}

// Generate returns n words drawn from the list using seed. Words are drawn
// independently, so a passage may repeat a word — the same behaviour as
// typing tests like monkeytype. It returns nil for n <= 0.
func Generate(seed int64, n int) []string {
	if n <= 0 {
		return nil
	}
	r := rand.New(rand.NewPCG(uint64(seed), streamConst))
	out := make([]string, n)
	for i := range out {
		out[i] = list[r.IntN(len(list))]
	}
	return out
}

// Passage returns n generated words joined by single spaces. This is the form
// the typing engine consumes.
func Passage(seed int64, n int) string {
	return strings.Join(Generate(seed, n), " ")
}

// NewSeed returns a random seed suitable for Generate. Callers that need every
// player to see the same passage should generate one seed and share it.
func NewSeed() int64 {
	return rand.Int64()
}
