// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	machineIDA = "DD2ONW2-OT6FOUM-JJDO76N-Y6LEUYT-TGX3PT4-QMOEMHV-QF2C7ET-5S2QSQH"
	machineIDB = "LWJQ3KS-5FWYWAL-WRA2LYN-UXWBZK6-SRCTIMX-R3E5BPX-A6K76KZ-YH7VUQK"
)

func booksPath(t *testing.T) string { return filepath.Join(t.TempDir(), "machines") }

func TestMachinesAddListRemove(t *testing.T) {
	book := booksPath(t)
	if code, out, e := runKH("machines", "--machines-file", book); code != 0 || !strings.Contains(out, "no machines") {
		t.Fatalf("empty list: exit %d: %s%s", code, out, e)
	}
	if code, out, e := runKH("machines", "add", "--machines-file", book, "luks-ubuntu", machineIDA); code != 0 {
		t.Fatalf("add: exit %d: %s%s", code, out, e)
	}
	if code, out, e := runKH("machines", "add", "--machines-file", book, "--discovery", "https://disco.example/v2/", "fedora", machineIDB); code != 0 {
		t.Fatalf("add with discovery: exit %d: %s%s", code, out, e)
	}
	code, out, _ := runKH("machines", "list", "--machines-file", book)
	for _, want := range []string{"luks-ubuntu", machineIDA, "fedora", machineIDB, "https://disco.example/v2/"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if code, out, e := runKH("machines", "remove", "--machines-file", book, "fedora"); code != 0 {
		t.Fatalf("remove: exit %d: %s%s", code, out, e)
	}
	if _, out, _ := runKH("machines", "--machines-file", book); strings.Contains(out, "fedora") || !strings.Contains(out, "luks-ubuntu") {
		t.Errorf("after remove:\n%s", out)
	}
	if code, _, e := runKH("machines", "remove", "--machines-file", book, "fedora"); code != 1 || !strings.Contains(e, "no machine named") {
		t.Errorf("removing what is not there: exit %d: %s", code, e)
	}
}

func TestMachinesRefusesNamesThatCouldBeMistakenForAnID(t *testing.T) {
	book := booksPath(t)
	for _, name := range []string{machineIDA, "", "two words", "-leading-dash", strings.Repeat("a", 65), "a/b"} {
		if code, _, e := runKH("machines", "add", "--machines-file", book, name, machineIDA); code != 2 || !strings.Contains(e, "name") {
			t.Errorf("%q: exit %d: %s", name, code, e)
		}
	}
	if code, _, e := runKH("machines", "add", "--machines-file", book, "box", "not-an-id"); code != 2 || !strings.Contains(e, "transport id") {
		t.Errorf("bad id: exit %d: %s", code, e)
	}
	if _, err := os.Stat(book); !os.IsNotExist(err) {
		t.Error("a refused add made the file")
	}
}

func TestMachinesAddDoesNotQuietlyChangeWhatANameMeans(t *testing.T) {
	book := booksPath(t)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "box", machineIDA)
	if code, _, e := runKH("machines", "add", "--machines-file", book, "box", machineIDA); code != 0 {
		t.Errorf("the same again should be fine: exit %d: %s", code, e)
	}
	if code, _, e := runKH("machines", "add", "--machines-file", book, "box", machineIDB); code != 1 || !strings.Contains(e, "--force") {
		t.Errorf("a different id for a name: exit %d: %s", code, e)
	}
	if code, _, e := runKH("machines", "add", "--machines-file", book, "--force", "box", machineIDB); code != 0 {
		t.Errorf("--force: exit %d: %s", code, e)
	}
	if _, out, _ := runKH("machines", "--machines-file", book); !strings.Contains(out, machineIDB) || strings.Contains(out, machineIDA) {
		t.Errorf("after --force:\n%s", out)
	}
}

func TestTheMachinesFileIsHandWritableAndKeepsComments(t *testing.T) {
	book := booksPath(t)
	text := "# my machines\n\nbox " + machineIDA + "\nfedora " + machineIDB + " https://disco.example/v2/\n"
	if err := os.WriteFile(book, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	ms, err := loadMachines(book)
	if err != nil || len(ms) != 2 || ms[1].Discovery != "https://disco.example/v2/" || ms[0].ID != machineIDA {
		t.Fatalf("%+v %v", ms, err)
	}
	if err := os.WriteFile(book, []byte("box not-an-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMachines(book); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("a bad line should be named by number: %v", err)
	}
}

func TestResolveMachineTakesANameOrAnID(t *testing.T) {
	book := booksPath(t)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "--discovery", "https://disco.example/v2/", "box", machineIDA)
	if m, err := resolveMachine(book, "box"); err != nil || m.ID != machineIDA || m.Discovery != "https://disco.example/v2/" {
		t.Errorf("by name: %+v %v", m, err)
	}
	// An id works with no book at all, and picks up a discovery server the book has for it.
	if m, err := resolveMachine(filepath.Join(t.TempDir(), "none"), machineIDB); err != nil || m.ID != machineIDB || m.Discovery != "" {
		t.Errorf("by id, no book: %+v %v", m, err)
	}
	if m, err := resolveMachine(book, machineIDA); err != nil || m.Discovery != "https://disco.example/v2/" {
		t.Errorf("by id, in the book: %+v %v", m, err)
	}
	if _, err := resolveMachine(book, "nope"); err == nil || !strings.Contains(err.Error(), "box") || !strings.Contains(err.Error(), "machines add") {
		t.Errorf("an unknown name should list the known ones and say how to add: %v", err)
	}
}

func TestUnlockByNameDialsTheMachineTheNameStandsFor(t *testing.T) {
	book := booksPath(t)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "box", machineIDA)
	keyFile, transportFile := keyholderDir(t)
	if code, _, e := runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	_, _, errOut := runKH("unlock", "--yes", "--timeout", "1s", "--direct-addr", "127.0.0.1:1", "--machines-file", book,
		"--key-file", keyFile, "--transport-key-file", transportFile, "box")
	if !strings.Contains(errOut, machineIDA) {
		t.Errorf("it did not dial the machine named box:\n%s", errOut)
	}
	if code, _, e := runKH("unlock", "--machines-file", book, "--key-file", keyFile, "--transport-key-file", transportFile, "nope"); code != 2 || !strings.Contains(e, "box") {
		t.Errorf("unknown name: exit %d: %s", code, e)
	}
}

func TestAnUnknownMachineNameSuggestsTheOneMeant(t *testing.T) {
	book := booksPath(t)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "luks-ubuntu2604", machineIDA)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "fedora", machineIDB)
	_, err := resolveMachine(book, "luks-ubunt")
	if err == nil || !strings.Contains(err.Error(), "did you mean luks-ubuntu2604?") {
		t.Errorf("%v", err)
	}
	if _, err := resolveMachine(book, "zebra"); err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("a suggestion for nothing like it: %v", err)
	}
}

// With one machine there is nothing to choose: "unlock" alone unlocks it.
func TestUnlockWithNoArgumentUnlocksTheOnlyMachineKnown(t *testing.T) {
	book := booksPath(t)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "box", machineIDA)
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	_, _, errOut := runKH("unlock", "--yes", "--timeout", "1s", "--direct-addr", "127.0.0.1:1", "--machines-file", book,
		"--key-file", keyFile, "--transport-key-file", transportFile)
	if !strings.Contains(errOut, machineIDA) {
		t.Errorf("it did not go for the only machine:\n%s", errOut)
	}
}

func TestUnlockWithNoArgumentAmongSeveralAsksWhichAndListsThem(t *testing.T) {
	book := booksPath(t)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "box", machineIDA)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "fedora", machineIDB)
	code, _, errOut := runKH("unlock", "--machines-file", book)
	if code != 2 || !strings.Contains(errOut, "which machine") || !strings.Contains(errOut, "box") || !strings.Contains(errOut, "fedora") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

func TestUnlockWithNoArgumentAndNoMachinesSaysHowToGetOne(t *testing.T) {
	code, _, errOut := runKH("unlock", "--machines-file", booksPath(t))
	if code != 2 || !strings.Contains(errOut, "synctang-keyholder pair") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

func TestAMisspeltCommandSuggestsTheOneMeant(t *testing.T) {
	code, _, errOut := runKH("unlok")
	if code != 2 || !strings.Contains(errOut, "did you mean \"unlock\"?") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}
