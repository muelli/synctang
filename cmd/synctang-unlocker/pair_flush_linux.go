// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package main

import (
	"bufio"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// dropTypedAhead throws away what the operator typed before the question was asked: what the buffered reader has
// already taken, and, when the answers come from a terminal, what is still queued in it. For input that is not
// a terminal (a pipe, a file) it leaves everything as it is: those answers were given on purpose.
func dropTypedAhead(r *bufio.Reader, source io.Reader) {
	f, ok := source.(*os.File)
	if !ok || !isTerminal(f) {
		return
	}
	_, _ = r.Discard(r.Buffered())
	_ = unix.IoctlSetInt(int(f.Fd()), unix.TCFLSH, unix.TCIFLUSH)
}
