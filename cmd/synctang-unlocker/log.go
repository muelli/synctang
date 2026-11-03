// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/coreos/go-systemd/v22/journal"
)

// logLevelTrace sits below slog's own lowest built-in level (Debug,
// -4) so --log-level trace genuinely shows more than debug does, not
// just a renamed copy of it. There is no standard syslog/journal
// priority for it either: it is sent at PriDebug, disambiguated by
// the SYNCTANG_LEVEL field journalHandler adds to every entry.
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

// newLogger returns a structured logger at level. Entries go to the
// systemd journal (queried later with journalctl, filterable by
// SYNCTANG_LEVEL and every other structured field) when the journal
// socket is reachable, which it is inside a dracut initrd's own
// systemd instance regardless of how this process's own stdout and
// stderr happen to be wired; otherwise a plain text fallback goes to
// stderr, so this still works under "go test" or a bare shell with no
// systemd at all.
func newLogger(level slog.Level) *slog.Logger {
	return newLoggerTo(level, journal.Enabled(), os.Stderr)
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

// newLoggerTo builds the logger, with the journal and the console as
// separate, explicit decisions so both can be tested without a
// systemd.
func newLoggerTo(level slog.Level, journalEnabled bool, console io.Writer) *slog.Logger {
	if !journalEnabled {
		return slog.New(slog.NewTextHandler(console, &slog.HandlerOptions{Level: level}))
	}
	consoleFloor := level
	if consoleFloor < consoleLevel {
		consoleFloor = consoleLevel
	}
	return slog.New(teeHandler{
		journal: newJournalHandler(level),
		console: slog.NewTextHandler(console, &slog.HandlerOptions{Level: consoleFloor}),
	})
}

// teeHandler writes each record to both the journal, which keeps
// everything for afterwards, and the console, which is all a human has
// while the machine is still locked.
type teeHandler struct {
	journal slog.Handler
	console slog.Handler
}

func (h teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.journal.Enabled(ctx, level) || h.console.Enabled(ctx, level)
}

func (h teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var err error
	if h.journal.Enabled(ctx, r.Level) {
		err = h.journal.Handle(ctx, r)
	}
	if h.console.Enabled(ctx, r.Level) {
		// Deliberately not short-circuited by a journal failure: the
		// console is the more important of the two here.
		if cerr := h.console.Handle(ctx, r.Clone()); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

func (h teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return teeHandler{journal: h.journal.WithAttrs(attrs), console: h.console.WithAttrs(attrs)}
}

func (h teeHandler) WithGroup(name string) slog.Handler {
	return teeHandler{journal: h.journal.WithGroup(name), console: h.console.WithGroup(name)}
}

// journalHandler sends every record to the systemd journal via its
// native protocol (github.com/coreos/go-systemd/v22/journal), one
// structured field per attribute, prefixed SYNCTANG_ so they do not
// collide with the journal's own reserved field names.
type journalHandler struct {
	level slog.Level
	attrs []slog.Attr
	group string
}

func newJournalHandler(level slog.Level) *journalHandler {
	return &journalHandler{level: level}
}

func (h *journalHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *journalHandler) Handle(_ context.Context, r slog.Record) error {
	vars := map[string]string{"SYNCTANG_LEVEL": string(levelName(r.Level))}
	addVar := func(a slog.Attr) {
		key := "SYNCTANG_" + strings.ToUpper(strings.ReplaceAll(h.group+a.Key, ".", "_"))
		vars[key] = a.Value.String()
	}
	for _, a := range h.attrs {
		addVar(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		addVar(a)
		return true
	})
	return journal.Send(r.Message, journalPriority(r.Level), vars)
}

func (h *journalHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &journalHandler{level: h.level, attrs: merged, group: h.group}
}

func (h *journalHandler) WithGroup(name string) slog.Handler {
	return &journalHandler{level: h.level, attrs: h.attrs, group: h.group + name + "."}
}

func journalPriority(level slog.Level) journal.Priority {
	switch {
	case level >= slog.LevelError:
		return journal.PriErr
	case level >= slog.LevelWarn:
		return journal.PriWarning
	case level >= slog.LevelInfo:
		return journal.PriInfo
	default:
		return journal.PriDebug // covers both debug and trace
	}
}
