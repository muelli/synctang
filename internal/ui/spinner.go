// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"io"
	"os"
	"sync"
	"time"
)

var (
	unicodeFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	plainFrames   = []string{"|", "/", "-", `\`}
)

// Spin shows a turning spinner and what label says, on one line that is redrawn in place, until the
// function it returns is called, which clears the line. label is asked again at every turn, so it can
// say how long has passed. It does nothing unless w is a terminal: to a pipe it writes nothing, and the
// caller says in a line of its own what is being waited for.
func Spin(w io.Writer, label func() string) (stop func()) {
	t := os.Getenv("TERM")
	return spin(w, label, isTerminal(w) && t != "" && t != "dumb", For(w), 100*time.Millisecond)
}

func spin(w io.Writer, label func() string, live bool, s Style, every time.Duration) func() {
	if !live {
		return func() {}
	}
	frames := plainFrames
	if s.Unicode {
		frames = unicodeFrames
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for i := 0; ; i++ {
			// The cursor goes back to the start of the line: a library that logs to the terminal on its own
			// then writes over the spinner, from the left, instead of after it.
			_, _ = io.WriteString(w, "\r"+s.Cyan(frames[i%len(frames)])+" "+label()+"\x1b[K\r")
			select {
			case <-done:
				_, _ = io.WriteString(w, "\r\x1b[K")
				return
			case <-tick.C:
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-finished
	}
}

// Status is the line a program redraws while it waits, and replaces when what it waits for changes. It
// is for a person at a terminal: to anything else it is not live, writes nothing, and the program says
// in ordinary lines what it would have shown.
type Status struct {
	w     io.Writer
	live  bool
	s     Style
	every time.Duration
	stop  func()
}

// NewStatus is a Status on w (usually standard error).
func NewStatus(w io.Writer) *Status {
	t := os.Getenv("TERM")
	return newStatus(w, isTerminal(w) && t != "" && t != "dumb", For(w), 100*time.Millisecond)
}

func newStatus(w io.Writer, live bool, s Style, every time.Duration) *Status {
	return &Status{w: w, live: live, s: s, every: every}
}

// Live says whether Set shows anything.
func (st *Status) Live() bool { return st.live }

// Set shows label (asked again at every turn, so it can say how long has passed) in place of what was shown.
func (st *Status) Set(label func() string) {
	st.Stop()
	st.stop = spin(st.w, label, st.live, st.s, st.every)
}

// Stop clears the line. It is harmless to call again, and before printing anything else.
func (st *Status) Stop() {
	if st.stop != nil {
		st.stop()
		st.stop = nil
	}
}
