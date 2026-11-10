// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wrap breaks prose into lines that fit a terminal. A line longer than the terminal is wide
// breaks wherever the terminal chooses and the rest starts at the left margin, which reads as a new
// line of output; so prose a program prints is broken here, at words, under the indent it belongs to.
// What cannot be broken (a path, a key, a Device ID) stays whole on a line of its own.
package wrap

import "strings"

// Paragraph breaks text into lines of at most width columns, each starting with indent.
func Paragraph(text, indent string, width int) string {
	return HangingParagraph(indent, text, width)
}

// HangingParagraph is Paragraph with a first line that starts with prefix and continuation lines that
// are indented as far as the prefix is long.
func HangingParagraph(prefix, text string, width int) string {
	hang := strings.Repeat(" ", len(prefix))
	var lines []string
	line := ""
	start := prefix
	for _, word := range strings.Fields(text) {
		if line != "" && len(start)+len(line)+1+len(word) > width {
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
