// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/muelli/synctang/internal/journal"
)

// logLevelTrace sits below slog's own lowest built-in level (Debug,
// -4) so --log-level trace genuinely shows more than debug does, not
// just a renamed copy of it. There is no standard syslog/journal
// priority for it either: it is sent at priority 7 with debug.
const levelTrace = slog.Level(-8)

// logLevelName is one of the five levels this project's agent
// supports, matching syncthing-socket's own --log-level convention
// exactly (trace, debug, info, warn, error), so anyone who has used
// that tool already knows what to expect here.
type logLevelName string

const (
	logLevelTrace logLevelName = "trace"
	logLevelDebug logLevelName = "debug"
	logLevelInfo  logLevelName = "info"
	logLevelWarn  logLevelName = "warn"
	logLevelError logLevelName = "error"
)

// parseLogLevel parses a --log-level value. An empty string means
// info, the sensible default for a long-running agent.
func parseLogLevel(s string) (slog.Level, error) {
	switch logLevelName(strings.ToLower(s)) {
	case logLevelTrace:
		return levelTrace, nil
	case logLevelDebug:
		return slog.LevelDebug, nil
	case logLevelInfo, "":
		return slog.LevelInfo, nil
	case logLevelWarn, "warning":
		return slog.LevelWarn, nil
	case logLevelError:
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q, want one of trace, debug, info, warn, error", s)
	}
}

// levelName is parseLogLevel's inverse, used to label journal entries
// and text-fallback output with a name a human recognises rather than
// a raw slog.Level integer.
func levelName(level slog.Level) logLevelName {
	switch {
	case level >= slog.LevelError:
		return logLevelError
	case level >= slog.LevelWarn:
		return logLevelWarn
	case level >= slog.LevelInfo:
		return logLevelInfo
	case level >= slog.LevelDebug:
		return logLevelDebug
	default:
		return logLevelTrace
	}
}

// journalMode says whether the agent writes to the systemd journal: "auto" when its
// socket exists, "on" always (and say so if it cannot), "off" never.
type journalMode string

const (
	journalAuto journalMode = "auto"
	journalOn   journalMode = "on"
	journalOff  journalMode = "off"
)

func parseJournalMode(s string) (journalMode, error) {
	switch journalMode(s) {
	case journalAuto, "":
		return journalAuto, nil
	case journalOn:
		return journalOn, nil
	case journalOff:
		return journalOff, nil
	}
	return "", fmt.Errorf("unknown journal mode %q, want one of auto, on, off", s)
}

// journalIdentifier is the SYSLOG_IDENTIFIER of the agent's entries, so that
// "journalctl -t synctang-unlocker" finds them. Without the field journald files them
// under the process's name, which the kernel cuts to fifteen characters.
const journalIdentifier = "synctang-unlocker"

// newLogger returns a structured logger at level for the agent. Entries go to the
// systemd journal through journald's own socket (queried later with journalctl,
// by -t synctang-unlocker and by field), and warnings and worse also go to console, which
// the dracut hook points at /dev/console. If the journal cannot be had, everything
// goes to console and that is said, once; the agent is never stopped for it.
func newLogger(level slog.Level, mode journalMode, socket string, console io.Writer) *slog.Logger {
	if mode == journalOff {
		return newLoggerTo(level, nil, console)
	}
	if mode == journalAuto {
		if info, err := os.Stat(socket); err != nil || info.Mode()&os.ModeSocket == 0 {
			return newLoggerTo(level, nil, console)
		}
	}
	conn, err := journal.Dial(socket, journalIdentifier)
	if err != nil {
		fmt.Fprintf(console, "synctang-unlocker: the journal is unavailable (%v); logging to the console only\n", err)
		return newLoggerTo(level, nil, console)
	}
	return newLoggerWithJournal(level, conn, console, func(err error) {
		fmt.Fprintf(console, "synctang-unlocker: the journal is unavailable (%v); logging to the console only\n", err)
	})
}

// consoleLevel is the level at or above which records also go to the
// console when the journal is in use.
//
// A machine sitting at its LUKS prompt is precisely the machine whose
// journal nobody can read: reading it needs the disk that is not
// unlocking. Sending everything to the journal alone therefore hides
// the failures that matter most, at the only moment they matter. The
// agent once failed to announce itself every ten seconds for hours
// while printing nothing whatsoever, and from the console it looked
// identical to a machine waiting patiently.
//
// Warning and above only: the console is a person staring at a boot
// prompt, not a log sink, and routine progress there would bury the
// one line worth reading.
const consoleLevel = slog.LevelWarn

// newLoggerTo builds the logger over an open journal connection (nil for none), so
// both choices can be tested without a systemd.
func newLoggerTo(level slog.Level, conn *journal.Conn, console io.Writer) *slog.Logger {
	return newLoggerWithJournal(level, conn, console, func(err error) {
		fmt.Fprintf(console, "synctang-unlocker: the journal is unavailable (%v); logging to the console only\n", err)
	})
}

func newLoggerWithJournal(level slog.Level, conn *journal.Conn, console io.Writer, onError func(error)) *slog.Logger {
	full := slog.NewTextHandler(console, &slog.HandlerOptions{Level: level})
	if conn == nil {
		return slog.New(full)
	}
	floor := level
	if floor < consoleLevel {
		floor = consoleLevel
	}
	return slog.New(teeHandler{
		journal:      journal.NewHandler(conn, level, onError),
		console:      slog.NewTextHandler(console, &slog.HandlerOptions{Level: floor}),
		consoleFull:  full,
		journalAlive: nil,
	}.withAlive())
}

// teeHandler writes each record to the journal, which keeps everything for afterwards,
// and to the console, which is all a human has while the machine is still locked. When
// the journal stops working, the console takes everything the level allows.
type teeHandler struct {
	journal      *journal.Handler
	console      slog.Handler // warnings and worse, while the journal works
	consoleFull  slog.Handler // everything the level allows, once it does not
	journalAlive func() bool
}

func (h teeHandler) withAlive() teeHandler {
	h.journalAlive = h.journal.Working
	return h
}

func (h teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if !h.journalAlive() {
		return h.consoleFull.Enabled(ctx, level)
	}
	return h.journal.Enabled(ctx, level) || h.console.Enabled(ctx, level)
}

func (h teeHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.journalAlive() && h.journal.Enabled(ctx, r.Level) {
		_ = h.journal.Handle(ctx, r.Clone())
	}
	// Judged after the write: the first failure of the journal is also the moment the
	// console takes over, and this record is not lost.
	if !h.journalAlive() {
		if h.consoleFull.Enabled(ctx, r.Level) {
			return h.consoleFull.Handle(ctx, r.Clone())
		}
		return nil
	}
	if h.console.Enabled(ctx, r.Level) {
		return h.console.Handle(ctx, r.Clone())
	}
	return nil
}

func (h teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return teeHandler{
		journal:      h.journal.WithAttrs(attrs).(*journal.Handler),
		console:      h.console.WithAttrs(attrs),
		consoleFull:  h.consoleFull.WithAttrs(attrs),
		journalAlive: h.journalAlive,
	}
}

func (h teeHandler) WithGroup(name string) slog.Handler {
	return teeHandler{
		journal:      h.journal.WithGroup(name).(*journal.Handler),
		console:      h.console.WithGroup(name),
		consoleFull:  h.consoleFull.WithGroup(name),
		journalAlive: h.journalAlive,
	}
}
