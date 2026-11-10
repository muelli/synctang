// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import "strings"

// Closest is the name a mistyped word most likely meant: the one a character or two away (or a third of
// the word, for longer ones), a different case of, or the only one it is the beginning of. Nothing when
// there is no good candidate, rather than a poor guess.
func Closest(word string, names []string) (string, bool) {
	lower := strings.ToLower(word)
	if len(lower) < 2 {
		return "", false
	}
	best, bestDistance := "", len(word)+1
	var prefixed []string
	for _, name := range names {
		n := strings.ToLower(name)
		if n == lower {
			return name, true
		}
		if strings.HasPrefix(n, lower) {
			prefixed = append(prefixed, name)
		}
		if d := distance(lower, n); d < bestDistance {
			best, bestDistance = name, d
		}
	}
	if len(prefixed) == 1 {
		return prefixed[0], true
	}
	if bestDistance <= max(1, len(lower)/3) && bestDistance < len(lower) {
		return best, true
	}
	return "", false
}

// distance is the Levenshtein distance: how many single-character edits turn a into b.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
