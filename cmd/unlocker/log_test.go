// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

func TestParseLogLevel(t *testing.T) {
	cases := map[string]struct {
		want    logLevelName
		wantErr bool
	}{
		"trace":   {logLevelTrace, false},
		"TRACE":   {logLevelTrace, false},
		"debug":   {logLevelDebug, false},
		"info":    {logLevelInfo, false},
		"":        {logLevelInfo, false},
		"warn":    {logLevelWarn, false},
		"warning": {logLevelWarn, false},
		"error":   {logLevelError, false},
		"bogus":   {"", true},
	}
	for input, c := range cases {
		t.Run(input, func(t *testing.T) {
			got, err := parseLogLevel(input)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseLogLevel(%q): expected an error, got level %v", input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLogLevel(%q): %v", input, err)
			}
			if levelName(got) != c.want {
				t.Fatalf("parseLogLevel(%q) = %v (name %q), want name %q", input, got, levelName(got), c.want)
			}
		})
	}
}

// Trace must be strictly below Debug, and each level strictly ordered
// below the next, or a --log-level filter would not behave as a human
// expects ("trace shows everything debug does, and more").
func TestLogLevelOrdering(t *testing.T) {
	trace, _ := parseLogLevel("trace")
	debug, _ := parseLogLevel("debug")
	info, _ := parseLogLevel("info")
	warn, _ := parseLogLevel("warn")
	errLevel, _ := parseLogLevel("error")

	levels := []struct {
		name  string
		level int
	}{
		{"trace", int(trace)},
		{"debug", int(debug)},
		{"info", int(info)},
		{"warn", int(warn)},
		{"error", int(errLevel)},
	}
	for i := 1; i < len(levels); i++ {
		if levels[i-1].level >= levels[i].level {
			t.Fatalf("%s (%d) must be strictly below %s (%d)",
				levels[i-1].name, levels[i-1].level, levels[i].name, levels[i].level)
		}
	}
}

func TestLevelNameCoversEveryStandardLevel(t *testing.T) {
	for _, name := range []string{"trace", "debug", "info", "warn", "error"} {
		level, err := parseLogLevel(name)
		if err != nil {
			t.Fatalf("parseLogLevel(%q): %v", name, err)
		}
		if got := levelName(level); string(got) != name {
			t.Fatalf("levelName(parseLogLevel(%q)) = %q, want %q", name, got, name)
		}
	}
}
