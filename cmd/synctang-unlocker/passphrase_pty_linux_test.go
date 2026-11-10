// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Against a real pseudo-terminal, which is what standard input is when a person types: the passphrase
// is read up to Enter, the prompt names it, and nothing typed is echoed back.
func TestReadingThePassphraseFromARealTerminalDoesNotEchoIt(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminals here: %v", err)
	}
	defer master.Close()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlocking the pty: %v", err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("naming the pty: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("opening the pty: %v", err)
	}
	defer slave.Close()

	oldStdin, oldNotice, oldTTY := os.Stdin, passphraseNotice, stdinIsTerminal
	defer func() { os.Stdin, passphraseNotice, stdinIsTerminal = oldStdin, oldNotice, oldTTY }()
	os.Stdin, passphraseNotice = slave, slave
	stdinIsTerminal = func() bool { return true }

	// What the terminal shows the person: read for as long as the master is open.
	var mu sync.Mutex
	var seen bytes.Buffer
	go func() {
		typed := false
		buf := make([]byte, 256)
		for {
			k, err := master.Read(buf)
			mu.Lock()
			seen.Write(buf[:k])
			ready := !typed && strings.Contains(seen.String(), "press Enter")
			mu.Unlock()
			if ready {
				typed = true
				// A person types after the prompt is up and the program has turned echo off; the prompt is
				// written before that, so wait for the terminal to say so.
				for i := 0; i < 100; i++ {
					if tio, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS); err == nil && tio.Lflag&unix.ECHO == 0 {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
				_, _ = master.Write([]byte("hunter2-secret\n"))
			}
			if err != nil {
				return
			}
		}
	}()

	got, err := readPassphrase("-")
	if err != nil || string(got) != "hunter2-secret" {
		t.Fatalf("%q %v", got, err)
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	out := seen.String()
	mu.Unlock()
	if strings.Contains(out, "hunter2-secret") {
		t.Errorf("the passphrase was echoed to the terminal:\n%q", out)
	}
}
