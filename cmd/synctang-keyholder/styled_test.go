// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/muelli/synctang/internal/ui"
)

// What a person sees at a terminal, asked for with --color=always: with the escapes taken out, the lines
// a person reads. (Plain output is checked by every other test, which runs with no terminal.)
func styled(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	t.Setenv("LANG", "en_GB.UTF-8")
	t.Setenv("LC_ALL", "")
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	code, out, errOut = runKH(append([]string{"--color=always"}, args...)...)
	return code, ui.StripEscapes(out), ui.StripEscapes(errOut)
}

func TestStatusAtATerminalMarksWhatIsGoodAndWhatIsNot(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	if code, _, e := runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	code, out, _ := styled(t, "status", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"🔑 key: held by file", "✓ readable: yes", "transport identity: "} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	raw := func() string {
		t.Setenv("LANG", "en_GB.UTF-8")
		t.Setenv("TERM", "xterm")
		_, o, _ := runKH("--color=always", "status", "--key-file", keyFile, "--transport-key-file", transportFile)
		return o
	}()
	if !strings.Contains(raw, "\x1b[32m") {
		t.Errorf("nothing is green:\n%q", raw)
	}
}

func TestStatusAtATerminalSaysWhenTheKeyCannotBeRead(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	if err := os.Remove(keyFile); err != nil {
		t.Fatal(err)
	}
	// The record still names the file store, which can no longer be read: that is said, in red.
	code, out, _ := styled(t, "status", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 || !strings.Contains(out, "✗ readable: no (") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestInitAtATerminalLeadsWithTheKeyAndShowsTheCommandsToRun(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	code, out, _ := styled(t, "init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"✓ 🔑 generated a new key, held by file", "S: ", "transport-id: ", "synctang-unlocker pair", "synctang-keyholder pair '<link>'", "synctang-unlocker enrol"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}

func TestMachinesAtATerminalIsATableWithAHeader(t *testing.T) {
	book := booksPath(t)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "box", machineIDA)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "--discovery", "https://d.example/v2/", "a-longer-name", machineIDB)
	code, out, _ := styled(t, "machines", "--machines-file", book)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") || !strings.Contains(lines[0], "TRANSPORT ID") {
		t.Fatalf("%q", lines)
	}
	col := strings.Index(lines[0], "TRANSPORT ID")
	for _, l := range lines[1:] {
		if strings.Index(l, machineIDA) != col && strings.Index(l, machineIDB) != col {
			t.Errorf("the transport id is not under its header (column %d): %q", col, l)
		}
	}
}
