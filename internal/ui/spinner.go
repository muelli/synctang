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
			_, _ = io.WriteString(w, "\r"+s.Cyan(frames[i%len(frames)])+" "+label()+"\x1b[K")
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
