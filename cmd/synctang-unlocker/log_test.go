// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

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

// A machine sitting at its LUKS prompt is the one machine whose
// journal nobody can read: reading it needs the disk that is not
// unlocking. So when the journal is in use, anything at warning level
// or above must also reach stderr, which the dracut hook points at
// /dev/console.
//
// Without this the agent could fail to announce itself every ten
// seconds, for hours, and print nothing at all: the console showed
// "waiting for a key holder" and then silence, while the machine was
// unreachable to every key holder in the world. Diagnosing that took
// an afternoon of inference from outside, because the one component
// that knew what was wrong was writing it somewhere unreadable.
func TestWarningsReachTheConsoleWhenTheJournalIsInUse(t *testing.T) {
	var console bytes.Buffer
	logger := newLoggerTo(slog.LevelInfo, true, &console)

	logger.Warn("announce failed", "error", "nope")
	if !strings.Contains(console.String(), "announce failed") {
		t.Errorf("a warning must reach the console, got %q", console.String())
	}
}

// The console is a human staring at a boot prompt, not a log sink.
// Routine progress belongs in the journal only, or the warning that
// matters scrolls away amongst it.
func TestRoutineRecordsDoNotReachTheConsole(t *testing.T) {
	var console bytes.Buffer
	logger := newLoggerTo(slog.LevelInfo, true, &console)

	logger.Info("starting a recovery attempt")
	if strings.Contains(console.String(), "starting a recovery attempt") {
		t.Errorf("info records should stay in the journal, got %q", console.String())
	}
}

// With no journal, stderr is the only sink and gets everything the
// configured level allows, exactly as before.
func TestWithoutAJournalEverythingGoesToStderr(t *testing.T) {
	var console bytes.Buffer
	logger := newLoggerTo(slog.LevelInfo, false, &console)

	logger.Info("starting a recovery attempt")
	if !strings.Contains(console.String(), "starting a recovery attempt") {
		t.Errorf("without a journal, info must still be logged, got %q", console.String())
	}
}
