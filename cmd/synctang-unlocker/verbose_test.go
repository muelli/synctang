// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

// The programs that pair, open and rehearse show what the transport is doing only at warning level, with no way to
// ask for more. --verbose (or SYNCTANG_DEBUG=1), anywhere on the line as for the key holder, is that way: the log
// is turned up to trace, which is what "agent --log-level trace" gives the agent.

func traceEnabled() bool { return slog.Default().Enabled(context.Background(), levelTrace) }

func withDefaultLogger(t *testing.T) {
	t.Helper()
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelInfo})))
}

func TestVerboseTurnsTheLogUpToTrace(t *testing.T) {
	for name, args := range map[string][]string{
		"first":  {"--verbose", "completion", "bash"},
		"last":   {"completion", "bash", "--verbose"},
		"single": {"-verbose", "completion", "bash"},
	} {
		t.Run(name, func(t *testing.T) {
			withDefaultLogger(t)
			t.Setenv("SYNCTANG_DEBUG", "")
			var out, errOut bytes.Buffer
			if code := run(args, &out, &errOut); code != 0 {
				t.Fatalf("exit %d: %s", code, errOut.String())
			}
			if !traceEnabled() {
				t.Error("the log was not turned up to trace")
			}
		})
	}
}

func TestWithoutVerboseTheLogIsLeftAlone(t *testing.T) {
	withDefaultLogger(t)
	t.Setenv("SYNCTANG_DEBUG", "")
	var out, errOut bytes.Buffer
	if code := run([]string{"completion", "bash"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if traceEnabled() {
		t.Error("trace logging without being asked")
	}
}

func TestTheEnvironmentAsksForTheDebugLogToo(t *testing.T) {
	withDefaultLogger(t)
	t.Setenv("SYNCTANG_DEBUG", "1")
	var out, errOut bytes.Buffer
	if code := run([]string{"completion", "bash"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if !traceEnabled() {
		t.Error("SYNCTANG_DEBUG=1 did not turn the log up")
	}
}

func TestVerboseIsInTheUsageAndTheAgentTakesItAsTrace(t *testing.T) {
	if !bytesContains(usage, "--verbose") {
		t.Error("the usage text does not mention --verbose")
	}
	if got := agentLogLevel(string(logLevelInfo), true); got != logLevelTrace {
		t.Errorf("agent --verbose is %q, want trace", got)
	}
	if got := agentLogLevel("debug", true); got != "debug" {
		t.Errorf("an explicit --log-level must win: %q", got)
	}
	if got := agentLogLevel(string(logLevelInfo), false); got != logLevelInfo {
		t.Errorf("without --verbose: %q", got)
	}
}

func bytesContains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
