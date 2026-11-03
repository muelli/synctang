// SPDX-License-Identifier: AGPL-3.0-or-later

// Package journal writes log records to the systemd journal through journald's
// native socket protocol, using nothing outside the standard library.
//
// It exists for the agent in the initrd. There journald is already running, so the
// agent can talk to it directly; what is needed is the right SYSLOG_IDENTIFIER (a
// process's name, as journald derives it, is cut to fifteen characters, and
// "synctang-unlocker" is longer, so without the field its entries are filed under a
// name nobody would search for), the right PRIORITY and one field per attribute.
//
// The protocol is datagrams of "NAME=value\n" lines. A value that contains a newline
// cannot be written that way, so it is written as "NAME\n", a 64-bit little-endian
// length, the value and "\n".
package journal

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// DefaultSocket is where journald listens.
const DefaultSocket = "/run/systemd/journal/socket"

// DefaultTimeout bounds one write. A journal that is not reading must not be able to
// hold up the agent, which has a machine to unlock.
const DefaultTimeout = time.Second

// maxValue is the most of one field that is sent; more is cut, with a note. A datagram
// has a size limit, and a log line that large is a mistake worth seeing, not worth losing
// the whole record over.
const maxValue = 32 << 10

// Field is one NAME=value of a journal entry.
type Field struct{ Name, Value string }

// Conn is a connection to the journal's socket.
type Conn struct {
	// Timeout bounds each write; zero means DefaultTimeout.
	Timeout time.Duration

	identifier string
	conn       *net.UnixConn
	mu         sync.Mutex
}

// Dial connects to the journal socket at path, for entries that carry identifier as
// their SYSLOG_IDENTIFIER. It fails if there is no socket there, which is how a caller
// finds out that there is no journal to write to.
func Dial(path, identifier string) (*Conn, error) {
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	return &Conn{identifier: identifier, conn: conn}, nil
}

// Close closes the connection.
func (c *Conn) Close() error { return c.conn.Close() }

// Send writes one entry. It never waits longer than the timeout.
func (c *Conn) Send(priority int, message string, fields []Field) error {
	data := Encode(priority, c.identifier, message, fields)
	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	_, err := c.conn.Write(data)
	return err
}

// Encode is one entry in the native protocol: PRIORITY, SYSLOG_IDENTIFIER, MESSAGE and
// then fields, which the caller has already given usable names (see FieldName).
func Encode(priority int, identifier, message string, fields []Field) []byte {
	var b bytes.Buffer
	appendField(&b, "PRIORITY", string(rune('0'+priority%10)))
	appendField(&b, "SYSLOG_IDENTIFIER", identifier)
	appendField(&b, "MESSAGE", message)
	for _, f := range fields {
		appendField(&b, f.Name, f.Value)
	}
	return b.Bytes()
}

func appendField(b *bytes.Buffer, name, value string) {
	if len(value) > maxValue {
		value = value[:maxValue] + " [cut]"
	}
	if !strings.ContainsAny(value, "\n\x00") {
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(value)
		b.WriteByte('\n')
		return
	}
	b.WriteString(name)
	b.WriteByte('\n')
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(len(value)))
	b.Write(n[:])
	b.WriteString(value)
	b.WriteByte('\n')
}

// FieldName turns an attribute key into a journal field name: upper case, anything
// outside A to Z, 0 to 9 and the underscore replaced by an underscore. It reports false
// for a key that cannot be one: empty, starting with an underscore (journald keeps those
// for itself and drops them from clients) or a digit, or one of the three fields this
// package sets.
func FieldName(key string) (string, bool) {
	name := []byte(strings.ToUpper(key))
	for i, c := range name {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			name[i] = '_'
		}
	}
	if len(name) == 0 || name[0] == '_' || (name[0] >= '0' && name[0] <= '9') {
		return "", false
	}
	switch string(name) {
	case "MESSAGE", "PRIORITY", "SYSLOG_IDENTIFIER":
		return "", false
	}
	return string(name), true
}

// Priority is the syslog priority for a slog level: trace and debug 7, info 6, warn 4,
// error 3.
func Priority(level slog.Level) int {
	switch {
	case level >= slog.LevelError:
		return 3
	case level >= slog.LevelWarn:
		return 4
	case level >= slog.LevelInfo:
		return 6
	default:
		return 7
	}
}

// Handler is a slog.Handler that writes to the journal.
type Handler struct {
	shared *handlerState
	level  slog.Leveler
	attrs  []slog.Attr
	group  string
}

// handlerState is what every derived handler shares: the connection, and whether it
// has failed.
type handlerState struct {
	conn    *Conn
	onError func(error)
	once    sync.Once
	failed  bool
	mu      sync.Mutex
}

// NewHandler returns a handler that sends records at or above level through c. The
// first write that fails is reported through onError (nil for nobody), once, and after
// that records are dropped: a journal that has gone away must not be tried on every line,
// and never makes Handle fail, since the agent has something more important to do.
func NewHandler(c *Conn, level slog.Leveler, onError func(error)) *Handler {
	return &Handler{shared: &handlerState{conn: c, onError: onError}, level: level}
}

// Working reports whether no write has failed yet.
func (h *Handler) Working() bool {
	h.shared.mu.Lock()
	defer h.shared.mu.Unlock()
	return !h.shared.failed
}

func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	if !h.Working() {
		return nil
	}
	var fields []Field
	var walk func(prefix string, a slog.Attr)
	walk = func(prefix string, a slog.Attr) {
		v := a.Value.Resolve()
		if v.Kind() == slog.KindGroup {
			p := prefix
			if a.Key != "" {
				p = prefix + a.Key + "."
			}
			for _, g := range v.Group() {
				walk(p, g)
			}
			return
		}
		if name, ok := FieldName(prefix + a.Key); ok {
			fields = append(fields, Field{name, v.String()})
		}
	}
	for _, a := range h.attrs {
		walk("", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		walk(h.group, a)
		return true
	})
	if err := h.shared.conn.Send(Priority(r.Level), r.Message, fields); err != nil {
		h.shared.mu.Lock()
		h.shared.failed = true
		h.shared.mu.Unlock()
		h.shared.once.Do(func() {
			if h.shared.onError != nil {
				h.shared.onError(err)
			}
		})
	}
	return nil
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr(nil), h.attrs...), qualify(h.group, attrs)...)
	return &c
}

func (h *Handler) WithGroup(name string) slog.Handler {
	c := *h
	if name != "" {
		c.group = h.group + name + "."
	}
	return &c
}

// qualify gives attributes added with WithAttrs the group that was open when they
// were added.
func qualify(group string, attrs []slog.Attr) []slog.Attr {
	if group == "" {
		return attrs
	}
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		a.Key = group + a.Key
		out[i] = a
	}
	return out
}
