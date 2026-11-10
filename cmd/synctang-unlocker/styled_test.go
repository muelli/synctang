// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muelli/synctang/internal/ui"
)

// What a person sees at a terminal, with the escapes taken out. Every other test of this package runs
// with no terminal and checks the plain output, which must stay what scripts have always read.
func atATerminal(t *testing.T) ui.Style {
	t.Helper()
	t.Setenv("LANG", "en_GB.UTF-8")
	t.Setenv("LC_ALL", "")
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	ui.SetMode(ui.Always)
	t.Cleanup(func() { ui.SetMode(ui.Auto) })
	return ui.Style{Color: true, Unicode: true}
}

func TestRehearsalChecksAtATerminalAreMarkedAndPlainOtherwise(t *testing.T) {
	checks := []rehearsalCheck{
		{Name: "the initrd carries the identity", Status: "ok"},
		{Name: "the agent is the installed one", Status: "problem", Detail: "run dracut --force"},
		{Name: "the kernel command line leaves the agent on", Status: "unchecked"},
	}
	var plain bytes.Buffer
	if got := printChecks(&plain, checks); got != 1 {
		t.Fatalf("%d problems", got)
	}
	if !strings.Contains(plain.String(), "  ok         the initrd carries the identity\n") || !strings.Contains(plain.String(), "  PROBLEM    the agent is the installed one\n") {
		t.Errorf("plain output changed:\n%s", plain.String())
	}

	atATerminal(t)
	var out bytes.Buffer
	printChecks(&out, checks)
	got := ui.StripEscapes(out.String())
	for _, want := range []string{"  ✓ the initrd carries the identity\n", "  ✗ the agent is the installed one\n", "    run dracut --force\n", "  - the kernel command line leaves the agent on (not checked)\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}

func TestDevicesAtATerminalHaveABoldHeaderAndColouredState(t *testing.T) {
	devs := []luksDevice{
		{Path: "/dev/vda3", UUID: "91ae", Size: 5 << 30, Version: "2", HasToken: true, KeyHolders: 2},
		{Path: "/dev/vdb1", UUID: "bbbb", Size: 1 << 30, Version: "2"},
	}
	if plain := formatDevices(devs); strings.Contains(plain, "\x1b") || !strings.HasPrefix(plain, "DEVICE") {
		t.Errorf("plain:\n%q", plain)
	}
	look := atATerminal(t)
	out := formatDevicesFor(devs, look)
	if !strings.Contains(out, "\x1b[1mDEVICE") || !strings.Contains(out, "\x1b[32m2 key holders") || !strings.Contains(out, "\x1b[33mno token") {
		t.Errorf("not styled as expected:\n%q", out)
	}
	stripped := ui.StripEscapes(out)
	lines := strings.Split(strings.TrimRight(stripped, "\n"), "\n")
	col := strings.Index(lines[0], "UUID")
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l[col:], "91ae") && !strings.HasPrefix(l[col:], "bbbb") {
			t.Errorf("the UUID column is not aligned (%d): %q", col, l)
		}
	}
}

func TestThePairingQuestionFramesTheCodeAtATerminal(t *testing.T) {
	var plain bytes.Buffer
	pairConfirmer(false, &plain, strings.NewReader("y\n"))("PEER", []byte{4, 1, 2, 3, 4, 5, 6, 7, 8}, "483 291")
	if !strings.Contains(plain.String(), "  code:          483 291\n") {
		t.Errorf("plain output changed:\n%s", plain.String())
	}
	atATerminal(t)
	var out bytes.Buffer
	pairConfirmer(false, &out, strings.NewReader("y\n"))("PEER", []byte{4, 1, 2, 3, 4, 5, 6, 7, 8}, "483 291")
	got := ui.StripEscapes(out.String())
	if !strings.Contains(got, "  code:\n      ┌─────────┐\n      │ 483 291 │\n      └─────────┘\n") {
		t.Errorf("no frame:\n%s", got)
	}
}

func TestStatusAtATerminalListsKeyHoldersInATableAndWarnsInYellow(t *testing.T) {
	pass := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, pass)
	if err := enrolRecipient(device, pass, newPublicKey(t), "box", "MACHINE-ID", testKH1); err != nil {
		t.Fatal(err)
	}
	if err := enrolRecipient(device, pass, newPublicKey(t), "box", "MACHINE-ID", ""); err != nil {
		t.Fatal(err)
	}
	atATerminal(t)
	var out, errOut bytes.Buffer
	machineKey := filepath.Join(t.TempDir(), "machine.pem")
	if code := run([]string{"--color=always", "status", "--device", device, "--machine-key-file", machineKey}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	raw := out.String()
	got := ui.StripEscapes(raw)
	for _, want := range []string{"TRANSPORT ID", "KEYSLOT", "KEY\n", testKH1} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(raw, "\x1b[33mWARNING:") {
		t.Errorf("the warning is not yellow:\n%q", raw)
	}
	if strings.Contains(got, "kid ") {
		t.Errorf("the plain row format is left in a table:\n%s", got)
	}
}
