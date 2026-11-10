// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ui is what the programs add to their output for a person at a terminal: colour, symbols,
// aligned tables and a spinner. None of it reaches a pipe or a file, where scripts read the output (and
// so do this project's tests), unless it is asked for with --color=always; NO_COLOR and --color=never
// switch it off at a terminal. Every symbol has the plain words it stands for, so a terminal that
// cannot draw it, or a locale that is not UTF-8, gets the words.
package ui

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/term"
)

// Mode is what --color says.
type Mode int

const (
	// Auto: colour and symbols at a terminal, and plain text anywhere else.
	Auto Mode = iota
	// Always: colour and symbols even where the output is not a terminal (to a pager, say).
	Always
	// Never: no colour.
	Never
)

var mode = Auto

// SetMode sets what --color said, for every writer asked about afterwards.
func SetMode(m Mode) { mode = m }

// ParseMode reads the value of --color.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(s) {
	case "auto", "tty", "if-tty":
		return Auto, nil
	case "always", "yes", "force", "on":
		return Always, nil
	case "never", "no", "none", "off":
		return Never, nil
	}
	return Auto, fmt.Errorf("--color wants auto, always or never, not %q", s)
}

// ExtractColorFlag takes --color (and -color) out of a command line, wherever it stands before a "--",
// so that it works before the command and after it without every command declaring it.
func ExtractColorFlag(args []string) (rest []string, m Mode, err error) {
	m = Auto
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append(rest, args[i:]...), m, nil
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if name != "--color" && name != "-color" {
			rest = append(rest, arg)
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return nil, Auto, fmt.Errorf("option '--color' requires an argument: auto, always or never")
			}
			i++
			value = args[i]
		}
		if m, err = ParseMode(value); err != nil {
			return nil, Auto, err
		}
	}
	return rest, m, nil
}

// Style says what may be added to the output of one writer.
type Style struct {
	Color   bool // ANSI colours and bold
	Unicode bool // symbols and emoji, where the plain words are not wanted
}

// For is the style for w: see forWriter.
func For(w io.Writer) Style { return forWriter(w, mode, os.Getenv, isTerminal(w)) }

// forWriter decides from the mode, the environment and whether w is a terminal.
func forWriter(_ io.Writer, m Mode, getenv func(string) string, tty bool) Style {
	termName := getenv("TERM")
	dumb := termName == "dumb"
	var s Style
	switch m {
	case Always:
		s.Color = true
	case Auto:
		s.Color = getenv("NO_COLOR") == "" && ((tty && termName != "" && !dumb) || forced(getenv("CLICOLOR_FORCE")))
	}
	s.Unicode = utf8Locale(getenv) && !dumb && termName != "linux" && (tty || m == Always)
	return s
}

func forced(v string) bool { return v != "" && v != "0" }

func utf8Locale(getenv func(string) string) bool {
	locale := getenv("LC_ALL")
	if locale == "" {
		locale = getenv("LC_CTYPE")
	}
	if locale == "" {
		locale = getenv("LANG")
	}
	locale = strings.ToLower(locale)
	return strings.Contains(locale, "utf-8") || strings.Contains(locale, "utf8")
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(f.Fd()))
}

func (s Style) paint(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Green(text string) string  { return s.paint("32", text) }
func (s Style) Red(text string) string    { return s.paint("31", text) }
func (s Style) Yellow(text string) string { return s.paint("33", text) }
func (s Style) Cyan(text string) string   { return s.paint("36", text) }
func (s Style) Bold(text string) string   { return s.paint("1", text) }
func (s Style) Dim(text string) string    { return s.paint("2", text) }

// OK, Fail and Warn mark a line as good, bad or worth a look: a check mark, a cross or a warning sign
// where symbols work, and plain (the words the output always had) where they do not. Green, red and yellow.
func (s Style) OK(plain string) string   { return s.Green(s.symbol("✓", plain)) }
func (s Style) Fail(plain string) string { return s.Red(s.symbol("✗", plain)) }
func (s Style) Warn(plain string) string { return s.Yellow(s.symbol("⚠", plain)) }

func (s Style) symbol(sym, plain string) string {
	if s.Unicode {
		return sym
	}
	return plain
}

// Emoji is an emoji and a space where symbols work, and plain where they do not (usually nothing).
func (s Style) Emoji(emoji, plain string) string {
	if s.Unicode {
		return emoji + " "
	}
	return plain
}

var escapes = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// StripEscapes removes the colour and cursor codes from text.
func StripEscapes(text string) string { return escapes.ReplaceAllString(text, "") }

// VisibleWidth is how many terminal columns text takes: escapes take none, an emoji takes two.
func VisibleWidth(text string) int {
	width := 0
	for _, r := range StripEscapes(text) {
		if r >= 0x1F000 {
			width += 2
		} else {
			width++
		}
	}
	return width
}

// Table lays out rows under a header, in columns as wide as what they hold (escapes and multi-byte
// characters measured by what shows), two spaces apart. The header is bold; nothing trails.
func Table(header []string, rows [][]string, s Style) string {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = VisibleWidth(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], VisibleWidth(cell))
			}
		}
	}
	var out strings.Builder
	line := func(cells []string, paint func(string) string) {
		var b strings.Builder
		for i, cell := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(paint(cell))
			if i < len(cells)-1 && i < len(widths) {
				b.WriteString(strings.Repeat(" ", widths[i]-VisibleWidth(cell)))
			}
		}
		out.WriteString(strings.TrimRight(b.String(), " ") + "\n")
	}
	line(header, s.Bold)
	for _, row := range rows {
		line(row, func(c string) string { return c })
	}
	return out.String()
}

var program string

// SetProgram names the program in the messages Errorf and Notef print.
func SetProgram(name string) { program = name }

// Errorf prints an error message to w as "program: message": at a terminal the prefix is red, with a cross.
// A message that ends in a newline is a line; to anything but a terminal it is the same text as always.
func Errorf(w io.Writer, format string, args ...any) {
	s := For(w)
	fmt.Fprint(w, s.Red(s.symbolPrefix("✗", program+":"))+" "+fmt.Sprintf(format, args...))
}

// Errorln is Errorf for a message that is not a format, and ends a line.
func Errorln(w io.Writer, msg string) { Errorf(w, "%s\n", msg) }

// Notef is Errorf for what is not an error but a person should read: yellow, with a warning sign.
func Notef(w io.Writer, format string, args ...any) {
	s := For(w)
	fmt.Fprint(w, s.Yellow(s.symbolPrefix("⚠", program+":"))+" "+fmt.Sprintf(format, args...))
}

// symbolPrefix is the symbol and a space before text where symbols work, and text alone where they do not.
func (s Style) symbolPrefix(sym, text string) string {
	if s.Unicode {
		return sym + " " + text
	}
	return text
}

// Line is mark, a space and text, or just text when there is no mark (the plain style has none).
func (s Style) Line(mark, text string) string {
	if mark == "" {
		return text
	}
	return mark + " " + text
}

// Infof is a line of ordinary output that carries the program's name: no colour, the same text as always.
func Infof(w io.Writer, format string, args ...any) {
	fmt.Fprint(w, program+": "+fmt.Sprintf(format, args...))
}

// Successln prints "program: message" as a line that went well: a check mark and green at a terminal.
func Successln(w io.Writer, msg string) {
	s := For(w)
	fmt.Fprint(w, s.Green(s.symbolPrefix("✓", program+":"))+" "+msg+"\n")
}

// CodeBox shows a short code where the eye goes: in a frame, bold, where symbols work, and as the indented
// line it always was where they do not. indent is put before every line.
func CodeBox(s Style, indent, code string) string {
	if !s.Unicode {
		return indent + code
	}
	bar := strings.Repeat("─", VisibleWidth(code)+2)
	return indent + "┌" + bar + "┐\n" + indent + "│ " + s.Bold(code) + " │\n" + indent + "└" + bar + "┘"
}

var verbose bool

// SetVerbose says whether Debugf prints.
func SetVerbose(v bool) { verbose = v }

// VerboseFromEnv is whether SYNCTANG_DEBUG asks for the debug log: set, and not 0.
func VerboseFromEnv() bool {
	v := os.Getenv("SYNCTANG_DEBUG")
	return v != "" && v != "0"
}

// ExtractVerboseFlag takes --verbose (and -verbose) out of a command line, wherever it stands before a "--".
func ExtractVerboseFlag(args []string) (rest []string, verbose bool) {
	for i, arg := range args {
		if arg == "--" {
			return append(rest, args[i:]...), verbose
		}
		if arg == "--verbose" || arg == "-verbose" {
			verbose = true
			continue
		}
		rest = append(rest, arg)
	}
	return rest, verbose
}

// Debugf prints one line of the debug log to w, as "program: debug: message", when --verbose (or
// SYNCTANG_DEBUG) asked for it. It says what the program is doing and where, never a secret.
func Debugf(w io.Writer, format string, args ...any) {
	if !verbose {
		return
	}
	s := For(w)
	fmt.Fprint(w, s.Dim(program+": debug:")+" "+fmt.Sprintf(format, args...))
}
