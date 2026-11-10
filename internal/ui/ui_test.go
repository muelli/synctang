// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"bytes"
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// What a program prints to a pipe or a file is plain text: scripts read it, and so do the tests of this
// project. Colour and symbols are for a person at a terminal, who can also switch them off.
func TestAPipeGetsPlainText(t *testing.T) {
	var buf bytes.Buffer
	if s := forWriter(&buf, Auto, env(map[string]string{"TERM": "xterm-256color", "LANG": "en_GB.UTF-8"}), false); s != (Style{}) {
		t.Errorf("a buffer: %+v", s)
	}
}

func TestATerminalGetsColourAndUnicodeUnlessSwitchedOff(t *testing.T) {
	utf8 := map[string]string{"TERM": "xterm-256color", "LANG": "en_GB.UTF-8"}
	for _, tc := range []struct {
		name string
		vars map[string]string
		mode Mode
		want Style
	}{
		{"a terminal", utf8, Auto, Style{Color: true, Unicode: true}},
		{"NO_COLOR", merge(utf8, "NO_COLOR", "1"), Auto, Style{Unicode: true}},
		{"NO_COLOR empty is not set", merge(utf8, "NO_COLOR", ""), Auto, Style{Color: true, Unicode: true}},
		{"a dumb terminal", merge(utf8, "TERM", "dumb"), Auto, Style{}},
		{"no TERM", map[string]string{"LANG": "en_GB.UTF-8"}, Auto, Style{Unicode: true, Color: false}},
		{"the Linux console", merge(utf8, "TERM", "linux"), Auto, Style{Color: true}},
		{"not a UTF-8 locale", map[string]string{"TERM": "xterm", "LANG": "C"}, Auto, Style{Color: true}},
		{"LC_ALL wins over LANG", map[string]string{"TERM": "xterm", "LANG": "C", "LC_ALL": "de_DE.utf8"}, Auto, Style{Color: true, Unicode: true}},
		{"--color=never", utf8, Never, Style{Unicode: true}},
		{"--color=never beats CLICOLOR_FORCE", merge(utf8, "CLICOLOR_FORCE", "1"), Never, Style{Unicode: true}},
		{"--color=always", utf8, Always, Style{Color: true, Unicode: true}},
		{"--color=always beats NO_COLOR", merge(utf8, "NO_COLOR", "1"), Always, Style{Color: true, Unicode: true}},
	} {
		if got := forWriter(nil, tc.mode, env(tc.vars), true); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestColourAndSymbolsCanBeForcedOnToAPipe(t *testing.T) {
	var buf bytes.Buffer
	vars := map[string]string{"TERM": "xterm", "LANG": "en_GB.UTF-8"}
	if s := forWriter(&buf, Always, env(vars), false); !s.Color || !s.Unicode {
		t.Errorf("--color=always to a pipe: %+v", s)
	}
	if s := forWriter(&buf, Auto, env(merge(vars, "CLICOLOR_FORCE", "1")), false); !s.Color {
		t.Errorf("CLICOLOR_FORCE to a pipe: %+v", s)
	}
}

func merge(base map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"auto": Auto, "always": Always, "never": Never, "yes": Always, "no": Never, "force": Always, "none": Never} {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	if _, err := ParseMode("sometimes"); err == nil || !strings.Contains(err.Error(), "auto") {
		t.Errorf("a bad mode: %v", err)
	}
}

func TestExtractColorFlagFromAnywhereInTheArguments(t *testing.T) {
	for _, tc := range []struct {
		args []string
		rest []string
		mode Mode
		err  bool
	}{
		{[]string{"status"}, []string{"status"}, Auto, false},
		{[]string{"--color=never", "status"}, []string{"status"}, Never, false},
		{[]string{"status", "--color", "always", "--key-file", "k"}, []string{"status", "--key-file", "k"}, Always, false},
		{[]string{"-color=never", "status"}, []string{"status"}, Never, false},
		{[]string{"unlock", "--", "--color=never"}, []string{"unlock", "--", "--color=never"}, Auto, false},
		{[]string{"status", "--color"}, nil, Auto, true},
		{[]string{"status", "--color=purple"}, nil, Auto, true},
	} {
		rest, mode, err := ExtractColorFlag(tc.args)
		if (err != nil) != tc.err {
			t.Errorf("%v: err %v", tc.args, err)
			continue
		}
		if err == nil && (strings.Join(rest, "\x00") != strings.Join(tc.rest, "\x00") || mode != tc.mode) {
			t.Errorf("%v: rest %v mode %v, want %v %v", tc.args, rest, mode, tc.rest, tc.mode)
		}
	}
}

func TestPaintingWrapsTextInEscapesOnlyWhenColoured(t *testing.T) {
	c, p := Style{Color: true}, Style{}
	if got := c.Green("ok"); got != "\x1b[32mok\x1b[0m" {
		t.Errorf("%q", got)
	}
	if got := p.Green("ok"); got != "ok" {
		t.Errorf("%q", got)
	}
	if got := c.Bold(""); got != "" {
		t.Errorf("an empty text was painted: %q", got)
	}
}

func TestMarksUseASymbolOnlyWhereUnicodeWorksAndAlwaysKeepTheirPlainWords(t *testing.T) {
	for _, tc := range []struct {
		s          Style
		ok, bad, w string
	}{
		{Style{}, "ok", "FAILED", "WARNING"},
		{Style{Color: true}, "\x1b[32mok\x1b[0m", "\x1b[31mFAILED\x1b[0m", "\x1b[33mWARNING\x1b[0m"},
		{Style{Unicode: true}, "✓", "✗", "⚠"},
		{Style{Color: true, Unicode: true}, "\x1b[32m✓\x1b[0m", "\x1b[31m✗\x1b[0m", "\x1b[33m⚠\x1b[0m"},
	} {
		if got := tc.s.OK("ok"); got != tc.ok {
			t.Errorf("%+v ok: %q want %q", tc.s, got, tc.ok)
		}
		if got := tc.s.Fail("FAILED"); got != tc.bad {
			t.Errorf("%+v fail: %q want %q", tc.s, got, tc.bad)
		}
		if got := tc.s.Warn("WARNING"); got != tc.w {
			t.Errorf("%+v warn: %q want %q", tc.s, got, tc.w)
		}
	}
	if got := (Style{Unicode: true}).Emoji("🔓", ""); got != "🔓 " {
		t.Errorf("emoji: %q", got)
	}
	if got := (Style{}).Emoji("🔓", ""); got != "" {
		t.Errorf("emoji without unicode: %q", got)
	}
}

func TestVisibleWidthIgnoresEscapesAndCountsCharactersNotBytes(t *testing.T) {
	if got := VisibleWidth("\x1b[32m✓ ok\x1b[0m"); got != 4 {
		t.Errorf("%d", got)
	}
	if got := VisibleWidth("plain"); got != 5 {
		t.Errorf("%d", got)
	}
}

func TestTableAlignsColumnsByWhatIsVisible(t *testing.T) {
	s := Style{Color: true, Unicode: true}
	got := Table([]string{"NAME", "KEYSLOT"}, [][]string{{s.OK("ok") + " box", "1"}, {"a-much-longer-name", "12"}}, s)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("%q", got)
	}
	strip := func(l string) string { return StripEscapes(l) }
	if strip(lines[1])[strings.Index(strip(lines[1]), "1"):] != "1" {
		t.Errorf("rows: %q", lines)
	}
	col := strings.Index(strip(lines[0]), "KEYSLOT")
	for _, l := range lines[1:] {
		if i := strings.LastIndexAny(strip(l), "12"); i < col {
			t.Errorf("a cell left of its column %d: %q", col, strip(l))
		}
	}
	if !strings.Contains(lines[0], "\x1b[1m") {
		t.Errorf("the header is not bold: %q", lines[0])
	}
	plain := Table([]string{"NAME", "KEYSLOT"}, [][]string{{"box", "1"}}, Style{})
	if plain != "NAME  KEYSLOT\nbox   1\n" {
		t.Errorf("plain: %q", plain)
	}
}

func TestErrorsAndNotesCarryTheProgramName(t *testing.T) {
	SetProgram("synctang-x")
	defer SetProgram("")
	var buf bytes.Buffer
	Errorf(&buf, "no key at %s\n", "/k")
	Notef(&buf, "using a file\n")
	Errorln(&buf, "bad thing")
	want := "synctang-x: no key at /k\nsynctang-x: using a file\nsynctang-x: bad thing\n"
	if buf.String() != want {
		t.Errorf("plain:\n%q\nwant\n%q", buf.String(), want)
	}
}

func TestErrorsAreRedAndNotesYellowAtATerminal(t *testing.T) {
	SetProgram("synctang-x")
	defer SetProgram("")
	SetMode(Always)
	defer SetMode(Auto)
	t.Setenv("LANG", "en_GB.UTF-8")
	t.Setenv("LC_ALL", "")
	t.Setenv("TERM", "xterm")
	var buf bytes.Buffer
	Errorf(&buf, "no key\n")
	Notef(&buf, "a note\n")
	if want := "\x1b[31m✗ synctang-x:\x1b[0m no key\n\x1b[33m⚠ synctang-x:\x1b[0m a note\n"; buf.String() != want {
		t.Errorf("%q\nwant %q", buf.String(), want)
	}
}

func TestCodeBoxFramesTheCodeOnlyWhereSymbolsWork(t *testing.T) {
	if got := CodeBox(Style{Unicode: true}, "  ", "483 291"); got != "  ┌─────────┐\n  │ 483 291 │\n  └─────────┘" {
		t.Errorf("%q", got)
	}
	if got := CodeBox(Style{}, "    ", "483 291"); got != "    483 291" {
		t.Errorf("%q", got)
	}
	if got := CodeBox(Style{Color: true, Unicode: true}, "", "1 2"); !strings.Contains(got, "\x1b[1m1 2\x1b[0m") {
		t.Errorf("the code is not bold: %q", got)
	}
}
