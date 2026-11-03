// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"strconv"
	"strings"
)

const maxKeyslots = 32

// parseSection scans dump for a top-level (unindented) heading line
// exactly "heading:" and returns the indented lines belonging to it,
// stopping at the next unindented heading or end of input. cryptsetup
// luksDump has no other structure worth a real parser for.
func parseSection(dump, heading string) []string {
	var lines []string
	inSection := false
	for _, line := range strings.Split(dump, "\n") {
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inSection = strings.TrimSpace(line) == heading+":"
			continue
		}
		if inSection {
			lines = append(lines, line)
		}
	}
	return lines
}

// entryIDs pulls the "  N: rest" entries directly under a section,
// skipping their tab-indented detail lines ("\tKey: ..."), and returns
// each N with its rest, trimmed.
func entryIDs(lines []string) []struct {
	id   int
	rest string
} {
	var entries []struct {
		id   int
		rest string
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "\t") {
			continue // a detail line, not an entry
		}
		idPart, rest, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(idPart))
		if err != nil {
			continue
		}
		entries = append(entries, struct {
			id   int
			rest string
		}{id, strings.TrimSpace(rest)})
	}
	return entries
}

// usedKeyslots returns the occupied keyslot IDs, ascending.
func usedKeyslots(dump string) []int {
	var ids []int
	for _, e := range entryIDs(parseSection(dump, "Keyslots")) {
		ids = append(ids, e.id)
	}
	return ids
}

// lowestFreeKeyslot returns the lowest unoccupied keyslot ID in
// [0, maxKeyslots), matching LUKS2's own slot numbering limit.
func lowestFreeKeyslot(dump string) (int, error) {
	used := make(map[int]bool)
	for _, id := range usedKeyslots(dump) {
		used[id] = true
	}
	for i := 0; i < maxKeyslots; i++ {
		if !used[i] {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no free keyslot: all %d slots are occupied", maxKeyslots)
}

// tokenIDsOfType returns the token IDs whose type matches tokenType,
// ascending. Used to find an existing mr-1 token (there is at most
// one; see the design plan) without disturbing tokens belonging to
// Clevis, systemd-tpm2, or anything else on the same header.
func tokenIDsOfType(dump, tokenType string) []int {
	var ids []int
	for _, e := range entryIDs(parseSection(dump, "Tokens")) {
		if e.rest == tokenType {
			ids = append(ids, e.id)
		}
	}
	return ids
}
