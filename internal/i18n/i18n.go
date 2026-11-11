// SPDX-License-Identifier: AGPL-3.0-or-later

// Package i18n marks the messages a program shows to a person, so that they can be collected into a
// catalogue and translated later.
//
// It is only a mark. T returns its argument as it is, with no catalogue, no locale and no dependency, so the
// output of the programs is the same bytes with or without it. The name is T, not _ as in C: in Go, _ is the
// blank identifier and cannot be called.
//
// What gets marked is the text that reaches a person (what is printed to the terminal, the text of an error
// that a command prints, a flag's description, the usage text). Not marked: keys, file names, protocol text
// that a machine reads, and log lines for the journal. package markcheck lists what is not marked, and a test
// in each program fails if a message is left out.
package i18n

// T marks message as text for a person, and returns it unchanged.
func T(message string) string { return message }
