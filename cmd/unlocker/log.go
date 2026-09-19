// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
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
	if journal.Enabled() {
		return slog.New(newJournalHandler(level))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
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
