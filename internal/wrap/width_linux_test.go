// SPDX-License-Identifier: AGPL-3.0-or-later

package wrap

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// pty opens a pseudo-terminal of the given size and returns its slave side, which is what a program's
// standard output is when it runs in a terminal.
func pty(t *testing.T, cols, rows uint16) *os.File {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminals here: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlocking the pty: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Skipf("naming the pty: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("opening the pty: %v", err)
	}
	t.Cleanup(func() { slave.Close() })
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows}); err != nil {
		t.Fatalf("setting the size: %v", err)
	}
	return slave
}

// The point of asking: a terminal that is 120 columns wide gets lines that use it (up to what is
// readable), and one that is 60 gets lines that fit it.
func TestWidthFollowsTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		cols uint16
		want int
	}{{80, 78}, {60, 58}, {120, 100}, {20, 40}} {
		if got := Width(pty(t, tc.cols, 24)); got != tc.want {
			t.Errorf("%d columns: Width = %d, want %d", tc.cols, got, tc.want)
		}
	}
}
