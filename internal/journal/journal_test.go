// SPDX-License-Identifier: AGPL-3.0-or-later

package journal

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// decode parses a datagram of journald's native protocol: "NAME=value\n", or, for
// a value that holds a newline, "NAME\n" then a 64-bit little-endian length, the value
// and "\n". It is the reader's side of what Encode writes, written independently of it.
func decode(t *testing.T, p []byte) map[string]string {
	t.Helper()
	fields := map[string]string{}
	for len(p) > 0 {
		nl := bytes.IndexByte(p, '\n')
		eq := bytes.IndexByte(p, '=')
		if nl < 0 {
			t.Fatalf("a field without a newline: %q", p)
		}
		if eq >= 0 && eq < nl {
			fields[string(p[:eq])] = string(p[eq+1 : nl])
			p = p[nl+1:]
			continue
		}
		name := string(p[:nl])
		p = p[nl+1:]
		if len(p) < 8 {
			t.Fatalf("a binary field %q with no length", name)
		}
		n := binary.LittleEndian.Uint64(p[:8])
		p = p[8:]
		if uint64(len(p)) < n+1 || p[n] != '\n' {
			t.Fatalf("a binary field %q whose value does not end where its length says", name)
		}
		fields[name] = string(p[:n])
		p = p[n+1:]
	}
	return fields
}

// fakeJournal is a datagram socket in a temporary directory standing in for
// /run/systemd/journal/socket.
type fakeJournal struct {
	path string
	conn *net.UnixConn
	mu   sync.Mutex
	got  [][]byte
}

func newFakeJournal(t *testing.T, read bool) *fakeJournal {
	t.Helper()
	// A short directory: unix socket paths are limited to about a hundred characters.
	dir, err := os.MkdirTemp("", "jr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "socket")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f := &fakeJournal{path: path, conn: conn}
	if read {
		go func() {
			buf := make([]byte, 1<<20)
			for {
				n, err := conn.Read(buf)
				if err != nil {
					return
				}
				f.mu.Lock()
				f.got = append(f.got, append([]byte(nil), buf[:n]...))
				f.mu.Unlock()
			}
		}()
	}
	return f
}

// next waits for the n'th datagram and decodes it.
func (f *fakeJournal) next(t *testing.T, n int) map[string]string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.got) >= n {
			p := f.got[n-1]
			f.mu.Unlock()
			return decode(t, p)
		}
		f.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("datagram %d never arrived", n)
	return nil
}

func TestEncodeUsesNameEqualsValueAndTheBinaryFormForNewlines(t *testing.T) {
	got := Encode(6, "synctang-unlocker", "one line", []Field{{"STAGE", "waiting"}})
	for _, want := range []string{"PRIORITY=6\n", "SYSLOG_IDENTIFIER=synctang-unlocker\n", "MESSAGE=one line\n", "STAGE=waiting\n"} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("%q is missing from %q", want, got)
		}
	}

	multi := "first line\nsecond line\n"
	got = Encode(4, "id", multi, nil)
	want := append([]byte("MESSAGE\n"), 0, 0, 0, 0, 0, 0, 0, 0)
	binary.LittleEndian.PutUint64(want[len("MESSAGE\n"):], uint64(len(multi)))
	want = append(want, multi...)
	want = append(want, '\n')
	if !bytes.Contains(got, want) {
		t.Errorf("the multi-line message is not in the binary form: %q", got)
	}
	if round := decode(t, got)["MESSAGE"]; round != multi {
		t.Errorf("round trip: %q", round)
	}
}

func TestFieldNamesAreUppercasedAndTheUnusableAreSkipped(t *testing.T) {
	for in, want := range map[string]string{
		"machine":    "MACHINE",
		"elapsed_ms": "ELAPSED_MS",
		"route.won":  "ROUTE_WON",
		"a-b c":      "A_B_C",
		"Already_UP": "ALREADY_UP",
		"x9":         "X9",
	} {
		got, ok := FieldName(in)
		if !ok || got != want {
			t.Errorf("FieldName(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "_trusted", "9lives", "-dash", "\n", "message", "PRIORITY", "syslog_identifier"} {
		if got, ok := FieldName(bad); ok {
			t.Errorf("FieldName(%q) = %q, want it skipped (empty, leading underscore or digit, or one journald sets itself)", bad, got)
		}
	}
}

func TestPriorityMapsSlogLevels(t *testing.T) {
	for level, want := range map[slog.Level]int{
		slog.Level(-8): 7, slog.LevelDebug: 7, slog.LevelInfo: 6, slog.LevelWarn: 4, slog.LevelError: 3, slog.Level(12): 3,
	} {
		if got := Priority(level); got != want {
			t.Errorf("Priority(%d) = %d, want %d", level, got, want)
		}
	}
}

func TestTheHandlerSendsEveryLevelWithItsPriorityAndFields(t *testing.T) {
	f := newFakeJournal(t, true)
	c, err := Dial(f.path, "synctang-unlocker")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	log := slog.New(NewHandler(c, slog.Level(-8), func(error) { t.Error("a send failed") }))

	log.Log(context.Background(), slog.Level(-8), "tracing", "step", 1)
	log.Info("waiting for a key holder", "transport_id", "ABC-DEF", "attempt", 3)
	log.Warn("announce failed", "error", "HTTP 500")
	log.Error("recovery failed", "device", "/dev/vda3")
	log.With("machine", "study").WithGroup("net").Info("grouped", "port", 22067)

	for n, want := range []struct {
		prio, msg string
		extra     map[string]string
	}{
		{"7", "tracing", map[string]string{"STEP": "1"}},
		{"6", "waiting for a key holder", map[string]string{"TRANSPORT_ID": "ABC-DEF", "ATTEMPT": "3"}},
		{"4", "announce failed", map[string]string{"ERROR": "HTTP 500"}},
		{"3", "recovery failed", map[string]string{"DEVICE": "/dev/vda3"}},
		{"6", "grouped", map[string]string{"MACHINE": "study", "NET_PORT": "22067"}},
	} {
		got := f.next(t, n+1)
		if got["PRIORITY"] != want.prio || got["MESSAGE"] != want.msg || got["SYSLOG_IDENTIFIER"] != "synctang-unlocker" {
			t.Errorf("datagram %d: %v", n+1, got)
		}
		for k, v := range want.extra {
			if got[k] != v {
				t.Errorf("datagram %d: field %s = %q, want %q (all: %v)", n+1, k, got[k], v, got)
			}
		}
	}
}

func TestAMultiLineMessageAndValueRoundTripThroughTheSocket(t *testing.T) {
	f := newFakeJournal(t, true)
	c, err := Dial(f.path, "synctang-unlocker")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	log := slog.New(NewHandler(c, slog.LevelInfo, func(error) { t.Error("a send failed") }))
	log.Info("line one\nline two", "output", "cryptsetup said:\nno key available\n")
	got := f.next(t, 1)
	if got["MESSAGE"] != "line one\nline two" || got["OUTPUT"] != "cryptsetup said:\nno key available\n" {
		t.Errorf("%q", got)
	}
}

func TestTheLevelFiltersWhatIsSent(t *testing.T) {
	f := newFakeJournal(t, true)
	c, _ := Dial(f.path, "id")
	defer c.Close()
	log := slog.New(NewHandler(c, slog.LevelWarn, nil))
	log.Info("quiet")
	log.Warn("loud")
	if got := f.next(t, 1); got["MESSAGE"] != "loud" {
		t.Errorf("first datagram %v", got)
	}
}

func TestDialFailsWhenThereIsNoSocket(t *testing.T) {
	if _, err := Dial(filepath.Join(t.TempDir(), "nothing"), "id"); err == nil {
		t.Error("dialled a socket that is not there")
	}
}

// A journal that goes away does not stop the agent: the first failed write is reported
// once, through onError, and later records are dropped without trying again.
func TestAFailedWriteIsReportedOnceAndNeverReturnsAnError(t *testing.T) {
	f := newFakeJournal(t, true)
	c, err := Dial(f.path, "id")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var reported []error
	log := slog.New(NewHandler(c, slog.LevelInfo, func(err error) { reported = append(reported, err) }))
	log.Info("fine")
	f.next(t, 1)
	f.conn.Close() // the journal goes away
	os.Remove(f.path)
	h := NewHandler(c, slog.LevelInfo, func(err error) { reported = append(reported, err) })
	for i := 0; i < 5; i++ {
		if err := h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "lost", 0)); err != nil {
			t.Errorf("Handle returned %v; a journal problem must never reach the agent", err)
		}
	}
	if len(reported) != 1 {
		t.Errorf("reported %d times, want once: %v", len(reported), reported)
	}
}

// A journal that is full does not hang the agent: the write has a deadline.
func TestAWriteToAFullSocketGivesUpAtTheDeadline(t *testing.T) {
	f := newFakeJournal(t, false) // nobody reads, so the queue fills
	c, err := Dial(f.path, "id")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Timeout = 150 * time.Millisecond
	payload := strings.Repeat("x", 30_000)
	start := time.Now()
	var failed bool
	for i := 0; i < 2000 && !failed; i++ {
		before := time.Now()
		if err := c.Send(6, payload, nil); err != nil {
			failed = true
			if took := time.Since(before); took > 2*time.Second {
				t.Errorf("one write took %s", took)
			}
		}
	}
	if !failed {
		t.Fatal("2000 large datagrams into a socket nobody reads never failed")
	}
	if time.Since(start) > 30*time.Second {
		t.Error("the writes hung")
	}
}
