package lobby

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
)

// NewPlayerID returns an identifier unique to one SSH session.
//
// Usernames cannot serve as identity here: any public key is accepted, so two
// people can connect as the same name, and one person can hold two sessions.
func NewPlayerID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; if it ever does, a colliding
		// player ID is far less bad than refusing the connection.
		return "player"
	}
	return hex.EncodeToString(b[:])
}

// normalizeCode makes join codes forgiving to type: case and surrounding
// whitespace do not matter.
func normalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// removeString deletes the first occurrence of s, preserving order.
func removeString(list []string, s string) []string {
	if i := slices.Index(list, s); i >= 0 {
		return slices.Delete(list, i, i+1)
	}
	return list
}

// sortSnapshots orders lobbies for display: joinable ones first, then by code
// so the list does not jump around between refreshes.
func sortSnapshots(list []Snapshot) {
	slices.SortFunc(list, func(a, b Snapshot) int {
		if a.Phase != b.Phase {
			return int(a.Phase) - int(b.Phase)
		}
		return strings.Compare(a.Code, b.Code)
	})
}
