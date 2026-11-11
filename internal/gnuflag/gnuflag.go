// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gnuflag makes the standard flag package speak the GNU way. The flag package
// accepts "--name" and "-name" alike, so what a person types already works, but it writes its
// help and its errors with a single dash. Handing a flag set Writer(w) as its output
// makes both say "--name", which is also how the manual pages spell every option.
package gnuflag

import (
	"github.com/muelli/synctang/internal/i18n"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	textwrap "github.com/muelli/synctang/internal/wrap"
)

// descriptionIndent is where the description of an option starts, under the option.
const descriptionIndent = "      "

var (
	// "  -name string" at the start of a line of PrintDefaults output: an option with a name
	// of two or more characters. The text of the usage that follows is left alone.
	optionLine = regexp.MustCompile(`(?m)^( {2})-([A-Za-z][A-Za-z0-9-]+)`)

	// "    \ttext": how the flag package indents a description under its option.
	descriptionLine = regexp.MustCompile(`(?m)^    \t(.*)$`)

	unknown       = regexp.MustCompile(`flag provided but not defined: -{1,2}(\S+)`)
	needsArgument = regexp.MustCompile(`flag needs an argument: -{1,2}(\S+)`)
	badValue      = regexp.MustCompile(`(invalid (?:boolean )?value (?:"[^"]*"|\S+)) for (?:flag )?-{1,2}([A-Za-z][A-Za-z0-9-]*)`)
)

// rewrite spells the options in text the way GNU tools do.
func rewrite(text string, width int) string {
	text = unknown.ReplaceAllString(text, i18n.T("unrecognised option '--$1'"))
	text = needsArgument.ReplaceAllString(text, i18n.T("option '--$1' requires an argument"))
	text = badValue.ReplaceAllString(text, i18n.T("$1 for option '--$2'"))
	text = optionLine.ReplaceAllString(text, "$1--$2")
	return descriptionLine.ReplaceAllStringFunc(text, func(line string) string {
		return wrap(strings.TrimPrefix(line, "    \t"), width)
	})
}

// wrap breaks a description into lines that fit, each indented under its option. A word longer than the
// line (a path, say) stays whole on a line of its own.
func wrap(description string, width int) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(description) {
		if line != "" && len(descriptionIndent)+utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > width {
			lines = append(lines, descriptionIndent+line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	lines = append(lines, descriptionIndent+line)
	return strings.Join(lines, "\n")
}

type writer struct{ w io.Writer }

// Writer returns a writer that passes everything to w with the options of a flag set's help
// and errors respelled with two dashes. The flag package writes each help entry and each
// error in a single call, so rewriting call by call sees them whole.
func Writer(w io.Writer) io.Writer { return writer{w} }

func (g writer) Write(p []byte) (int, error) {
	if _, err := g.w.Write([]byte(rewrite(string(p), textwrap.Width(g.w)))); err != nil {
		return 0, err
	}
	return len(p), nil
}
