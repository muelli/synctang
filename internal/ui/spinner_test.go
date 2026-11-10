// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// A spinner is for a person watching a terminal. Anywhere else it writes nothing at all: a log does not
// want a hundred lines of it, and the line that says what is going on is printed once by the caller.
func TestSpinnerWritesNothingToAPipe(t *testing.T) {
	var buf bytes.Buffer
	stop := spin(&buf, func() string { return "waiting" }, false, Style{}, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	stop()
	if buf.Len() != 0 {
		t.Errorf("wrote %q", buf.String())
	}
}

func TestSpinnerRedrawsOneLineAndClearsItWhenStopped(t *testing.T) {
	buf := &lockedBuffer{}
	n := 0
	stop := spin(buf, func() string { n++; return "waiting for the machine" }, true, Style{Unicode: true}, 5*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	stop()
	stop() // stopping twice is harmless
	out := buf.String()
	if !strings.Contains(out, "\r") || !strings.Contains(out, "waiting for the machine") {
		t.Errorf("no redraw: %q", out)
	}
	if strings.Count(out, "\n") != 0 {
		t.Errorf("the spinner started a new line: %q", out)
	}
	if !strings.HasSuffix(out, "\r\x1b[K") {
		t.Errorf("the line was not cleared at the end: %q", out)
	}
	frames := 0
	for _, f := range unicodeFrames {
		if strings.Contains(out, f) {
			frames++
		}
	}
	if frames < 2 {
		t.Errorf("the spinner did not turn: %q", out)
	}
	before := buf.String()
	time.Sleep(30 * time.Millisecond)
	if buf.String() != before {
		t.Error("it kept drawing after it was stopped")
	}
}

func TestSpinnerUsesPlainFramesWhereUnicodeDoesNotWork(t *testing.T) {
	buf := &lockedBuffer{}
	stop := spin(buf, func() string { return "x" }, true, Style{}, 5*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	stop()
	for _, f := range unicodeFrames {
		if strings.Contains(buf.String(), f) {
			t.Errorf("a braille frame in %q", buf.String())
		}
	}
	if !strings.ContainsAny(buf.String(), `|/-\`) {
		t.Errorf("no plain frame in %q", buf.String())
	}
}
