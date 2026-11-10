// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// A program that waits on standard input without saying so looks hung. Reading the passphrase from it
// says so, on standard error, where it cannot get into anything the passphrase is piped through.
func TestReadingThePassphraseFromAPipeSaysSoOnStderr(t *testing.T) {
	var notice bytes.Buffer
	restore := stubStdin(t, strings.NewReader("pipe passphrase\n"), &notice, false)
	defer restore()
	got, err := readPassphrase("-")
	if err != nil || string(got) != "pipe passphrase" {
		t.Fatalf("%q %v", got, err)
	}
	if !strings.Contains(notice.String(), "reading the passphrase from standard input") {
		t.Errorf("no notice: %q", notice.String())
	}
	if strings.Contains(notice.String(), "Ctrl") || strings.Contains(notice.String(), "Enter") {
		t.Errorf("a hint about typing, for input that is a pipe: %q", notice.String())
	}
}

// From a terminal the person types it: the notice says how it ends (Enter, as for any passphrase),
// what is typed is not shown, and the text is not read to the end of the stream, which would need Ctrl-D.
func TestReadingThePassphraseFromATerminalTellsThePersonHowAndHidesIt(t *testing.T) {
	var notice bytes.Buffer
	restore := stubTerminalPassphrase(t, &notice, "typed passphrase")
	defer restore()
	got, err := readPassphrase("-")
	if err != nil || string(got) != "typed passphrase" {
		t.Fatalf("%q %v", got, err)
	}
	for _, want := range []string{"passphrase", "hidden", "Enter"} {
		if !strings.Contains(notice.String(), want) {
			t.Errorf("the prompt lacks %q: %q", want, notice.String())
		}
	}
	if strings.Contains(notice.String(), "typed passphrase") {
		t.Error("the passphrase was written to the notice")
	}
}

func TestAFilePassphraseSaysNothing(t *testing.T) {
	var notice bytes.Buffer
	restore := stubStdin(t, strings.NewReader(""), &notice, false)
	defer restore()
	if _, err := readPassphrase("/nonexistent/passphrase"); err == nil {
		t.Fatal("no error")
	}
	if notice.Len() != 0 {
		t.Errorf("a notice for a file: %q", notice.String())
	}
}

// stubStdin makes readPassphrase read from r, as a pipe or as a terminal, and say what it does to notice.
func stubStdin(t *testing.T, r io.Reader, notice io.Writer, terminal bool) func() {
	t.Helper()
	oldIn, oldNotice, oldTTY := passphraseStdin, passphraseNotice, stdinIsTerminal
	passphraseStdin, passphraseNotice = r, notice
	stdinIsTerminal = func() bool { return terminal }
	return func() { passphraseStdin, passphraseNotice, stdinIsTerminal = oldIn, oldNotice, oldTTY }
}

// stubTerminalPassphrase makes standard input a terminal at which the person types text.
func stubTerminalPassphrase(t *testing.T, notice io.Writer, text string) func() {
	t.Helper()
	restore := stubStdin(t, strings.NewReader(""), notice, true)
	old := readTerminalPassword
	readTerminalPassword = func() ([]byte, error) { return []byte(text), nil }
	return func() { readTerminalPassword = old; restore() }
}
