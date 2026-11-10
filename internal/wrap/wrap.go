// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wrap breaks prose into lines that fit a terminal. A line longer than the terminal is wide
// breaks wherever the terminal chooses and the rest starts at the left margin, which reads as a new
// line of output; so prose a program prints is broken here, at words, under the indent it belongs to.
// What cannot be broken (a path, a key, a Device ID) stays whole on a line of its own.
package wrap

import (
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	// DefaultWidth is used when the output is not a terminal (a pipe, a file, a test) or its size
	// cannot be read: it fits an 80-column terminal.
	DefaultWidth = 78
	minWidth     = 40
	// maxWidth keeps lines readable on a very wide terminal.
	maxWidth = 100
)

// Width is how wide a line of prose may be for the terminal w writes to: two columns less than the
// terminal (a line that fills the last column breaks in some terminals), at least 40 and at most 100.
// For anything that is not a terminal, DefaultWidth.
func Width(w io.Writer) int {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return DefaultWidth
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return DefaultWidth
	}
	columns, _, err := term.GetSize(fd)
	if err != nil {
		return DefaultWidth
	}
	return forColumns(columns)
}

// forColumns is the width of prose for a terminal of that many columns.
func forColumns(columns int) int {
	if columns <= 0 {
		return DefaultWidth
	}
	return min(max(columns-2, minWidth), maxWidth)
}

// Paragraph breaks text into lines of at most width columns, each starting with indent.
func Paragraph(text, indent string, width int) string {
	return HangingParagraph(indent, text, width)
}

// HangingParagraph is Paragraph with a first line that starts with prefix and continuation lines that
// are indented as far as the prefix is long.
func HangingParagraph(prefix, text string, width int) string {
	hang := strings.Repeat(" ", utf8.RuneCountInString(prefix))
	var lines []string
	line := ""
	start := prefix
	for _, word := range strings.Fields(text) {
		if line != "" && utf8.RuneCountInString(start)+utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > width {
			lines = append(lines, start+line)
			start = hang
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	lines = append(lines, start+line)
	return strings.Join(lines, "\n")
}
