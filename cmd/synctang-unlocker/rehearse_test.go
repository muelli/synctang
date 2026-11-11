// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
)

const lsinitrdSample = `Image: /boot/initramfs-6.12.0.img: 28M
========================================================================
Early CPIO image
========================================================================
drwxr-xr-x   3 root     root            0 Jan  1  1970 .
========================================================================
Version: dracut-108-2.fc44

Arguments: --force

dracut modules:
systemd
crypt
network
synctang
base
========================================================================
-rwxr-xr-x   1 root     root       773 Nov  3 22:18 etc/synctang/machine.pem
`

func TestInitrdModulesAreReadFromTheListingsHeader(t *testing.T) {
	got, err := parseInitrdModules(lsinitrdSample)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "systemd crypt network synctang base" {
		t.Fatalf("modules = %v", got)
	}
	if _, err := parseInitrdModules("something else entirely\n"); err == nil {
		t.Error("output with no module list must be an error, not an empty list")
	}
}

// fakeRehearsal supplies everything the static checks read.
func fakeRehearsal(t *testing.T, modules []string, modErr error, installed, inInitrd string, cmdline string) {
	t.Helper()
	wasMod, wasPath, wasCmd := initrdModules, installedAgentPath, kernelCommandLine
	t.Cleanup(func() { initrdModules, installedAgentPath, kernelCommandLine = wasMod, wasPath, wasCmd })
	initrdModules = func() ([]string, error) { return modules, modErr }
	installedAgentPath = writeTemp(t, "installed-agent", installed)
	kernelCommandLine = func() (string, error) { return cmdline, nil }
	files := map[string]string{"etc/synctang/machine.pem": "IDENTITY"}
	if inInitrd != "" {
		files[initrdAgent] = inInitrd
	}
	fakeInitrd(t, files, nil)
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	id, _ := systemFiles(t, content, "")
	_ = name
	return id
}

// withStaticChecks replaces the checks of the initrd, so that command-level tests need none
// of dracut: nil means every check passes.
func withStaticChecks(t *testing.T, checks []rehearsalCheck) {
	t.Helper()
	was := staticChecks
	t.Cleanup(func() { staticChecks = was })
	staticChecks = func(string, string) []rehearsalCheck {
		if checks == nil {
			return []rehearsalCheck{{Name: "the initrd has the dracut modules", Status: "ok"}}
		}
		return checks
	}
}

func statusOf(checks []rehearsalCheck, contains string) string {
	for _, c := range checks {
		if strings.Contains(c.Name, contains) {
			return c.Status
		}
	}
	return "missing"
}

func TestAllStaticChecksPassForAWellBuiltInitrd(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeRehearsal(t, []string{"systemd", "network", "synctang"}, nil, "AGENT-BYTES", "AGENT-BYTES", "ro quiet")
	for _, c := range staticRehearsalChecks(id, conf) {
		if c.Status != "ok" {
			t.Errorf("%+v", c)
		}
	}
}

func TestAMissingModuleIsAProblemWithTheFix(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeRehearsal(t, []string{"systemd", "base"}, nil, "A", "A", "ro")
	checks := staticRehearsalChecks(id, conf)
	if statusOf(checks, "dracut modules") != "problem" {
		t.Fatalf("%+v", checks)
	}
	for _, c := range checks {
		if strings.Contains(c.Name, "dracut modules") && (!strings.Contains(c.Detail, "synctang") || !strings.Contains(c.Detail, "network") || !strings.Contains(c.Detail, "dracut --force")) {
			t.Errorf("the problem should name the missing modules and the fix: %q", c.Detail)
		}
	}
}

func TestAnAgentInTheInitrdThatIsNotTheInstalledOneIsAProblem(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeRehearsal(t, []string{"network", "synctang"}, nil, "NEW-AGENT", "OLD-AGENT", "ro")
	checks := staticRehearsalChecks(id, conf)
	if statusOf(checks, "agent") != "problem" {
		t.Fatalf("%+v", checks)
	}
	fakeRehearsal(t, []string{"network", "synctang"}, nil, "AGENT", "", "ro")
	if statusOf(staticRehearsalChecks(id, conf), "agent") != "problem" {
		t.Error("an initrd with no agent at all is a problem too")
	}
}

func TestTheKernelCommandLineThatSwitchesTheAgentOffIsAProblem(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeRehearsal(t, []string{"network", "synctang"}, nil, "A", "A", "ro rd.synctang=0 quiet")
	if statusOf(staticRehearsalChecks(id, conf), "command line") != "problem" {
		t.Fatalf("rd.synctang=0 must be reported")
	}
}

func TestWhatCannotBeLookedAtIsUncheckedNotOK(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeRehearsal(t, nil, errors.New("lsinitrd: command not found"), "A", "A", "ro")
	fakeInitrd(t, nil, errors.New("lsinitrd: command not found"))
	checks := staticRehearsalChecks(id, conf)
	for _, c := range checks {
		if c.Status == "ok" && !strings.Contains(c.Name, "command line") {
			t.Errorf("%+v: a check that could not be made must not say ok", c)
		}
	}
	if statusOf(checks, "dracut modules") != "unchecked" {
		t.Errorf("%+v", checks)
	}
}

// The live part: the same wait and exchange as "open", and nothing is opened.

func TestRehearseRecoversTheSecretAndMapsNothing(t *testing.T) {
	calls := fakeOpener(t)
	f := newOpenFixture(t)
	withStaticChecks(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	done := startOpen([]string{"rehearse", "--device", f.device, "--machine-key-file", f.machineKey, "--direct-addr", f.addr, "--timeout", "20s"})
	time.Sleep(300 * time.Millisecond)
	result := f.answer(t, ctx, f.s)
	r := waitFor(t, done)

	if !result.OK || r.code != 0 {
		t.Fatalf("result %+v, exit %d, stderr %q", result, r.code, r.stderr)
	}
	if len(calls()) != 0 {
		t.Errorf("a rehearsal maps nothing, but the volume was opened: %v", calls())
	}
	for _, want := range []string{"this machine's transport id", "rehearsal passed", "not tested"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.stdout)
		}
	}
}

func TestRehearseSaysWhatItCouldNotTestAndWhatToDoAboutIt(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)
	withStaticChecks(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := startOpen([]string{"rehearse", "--device", f.device, "--machine-key-file", f.machineKey, "--direct-addr", f.addr, "--timeout", "20s"})
	time.Sleep(300 * time.Millisecond)
	f.answer(t, ctx, f.s)
	r := waitFor(t, done)
	for _, want := range []string{"early boot", "DHCP", "console", "passphrase"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the summary should name what a reboot alone tests; lacks %q:\n%s", want, r.stdout)
		}
	}
}

func TestRehearseKeepsWaitingAfterAWrongAnswerAndFailsIfNobodyIsRight(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)
	withStaticChecks(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := startOpen([]string{"rehearse", "--device", f.device, "--machine-key-file", f.machineKey, "--direct-addr", f.addr, "--timeout", "4s"})
	time.Sleep(300 * time.Millisecond)
	other := newOpenFixture(t) // another key holder's scalar: not the one enrolled on f.device
	result := f.answer(t, ctx, other.s)
	r := waitFor(t, done)
	if result.OK || r.code != 1 || !strings.Contains(r.stderr, "nobody approved") {
		t.Errorf("result %+v, exit %d, stderr %q", result, r.code, r.stderr)
	}
	if strings.Contains(r.stdout, "rehearsal passed") {
		t.Errorf("a failed rehearsal must not say it passed:\n%s", r.stdout)
	}
}

func TestRehearseNeedsRootBeforeAnythingWaits(t *testing.T) {
	fakeOpener(t)
	geteuid = func() int { return 1000 }
	f := newOpenFixture(t)
	start := time.Now()
	r := waitFor(t, startOpen([]string{"rehearse", "--device", f.device, "--machine-key-file", f.machineKey, "--timeout", "30s"}))
	if r.code != 1 || !strings.Contains(r.stderr, "root") || time.Since(start) > 5*time.Second {
		t.Errorf("exit %d, stderr %q, after %s", r.code, r.stderr, time.Since(start))
	}
}

func TestInitrdOnlyChecksWithoutWaitingForAKeyHolder(t *testing.T) {
	fakeOpener(t)
	withStaticChecks(t, []rehearsalCheck{{Name: "the initrd has the dracut modules", Status: "ok"}})
	start := time.Now()
	r := waitFor(t, startOpen([]string{"rehearse", "--initrd-only"}))
	if r.code != 0 || time.Since(start) > 5*time.Second || !strings.Contains(r.stdout, "ok") {
		t.Errorf("exit %d after %s:\n%s%s", r.code, time.Since(start), r.stdout, r.stderr)
	}
	if strings.Contains(r.stdout, "waiting for a key holder") {
		t.Errorf("--initrd-only must not wait:\n%s", r.stdout)
	}
}

func TestAProblemInTheInitrdFailsTheRehearsalBeforeAnyoneIsAsked(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)
	withStaticChecks(t, []rehearsalCheck{{Name: "the initrd has the dracut modules", Status: "problem", Detail: "synctang is missing; run dracut --force"}})
	start := time.Now()
	r := waitFor(t, startOpen([]string{"rehearse", "--device", f.device, "--machine-key-file", f.machineKey, "--timeout", "30s"}))
	if r.code != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("exit %d after %s", r.code, time.Since(start))
	}
	if !strings.Contains(r.stdout+r.stderr, "PROBLEM") || !strings.Contains(r.stdout+r.stderr, "dracut --force") {
		t.Errorf("the problem and its fix should be shown:\n%s%s", r.stdout, r.stderr)
	}
	if strings.Contains(r.stdout, "waiting for a key holder") {
		t.Errorf("nobody should be asked to approve a rehearsal that cannot pass:\n%s", r.stdout)
	}
}

func TestPairOffersARehearsal(t *testing.T) {
	hint := rehearsalHint("/dev/vda3")
	if !strings.Contains(hint, "synctang-unlocker rehearse --device /dev/vda3") || !strings.Contains(hint, "without rebooting") {
		t.Errorf("hint = %q", hint)
	}
}

// dracut copies binaries into the initrd and may strip them on the way, so the agent in the
// initrd is not byte for byte the installed file even when it is the same program. What is the
// same is what gets loaded.

func TestAStrippedCopyOfAProgramIsTheSameAgent(t *testing.T) {
	strip, err := exec.LookPath("strip")
	if err != nil {
		t.Skip("no strip")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	stripped := filepath.Join(t.TempDir(), "stripped")
	if out, err := exec.Command(strip, "--strip-all", "-o", stripped, exe).CombinedOutput(); err != nil {
		t.Skipf("strip: %v\n%s", err, out)
	}
	copyOfIt, err := os.ReadFile(stripped)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(original, copyOfIt) {
		t.Skip("strip changed nothing, so this proves nothing")
	}
	if !sameAgent(copyOfIt, original) {
		t.Errorf("the same program, stripped, must be the same agent")
	}
}

func TestADifferentProgramIsNotTheSameAgent(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.NewFile(bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]byte(nil), original...)
	for _, sec := range f.Sections {
		if sec.Name == ".text" {
			changed[sec.Offset+50] ^= 0xff // a byte of the program's code
			break
		}
	}
	if sameAgent(changed, original) {
		t.Error("a program with a changed byte in a loaded segment is not the same agent")
	}
}

func TestSomethingThatIsNotAnELFFileIsComparedByItsBytes(t *testing.T) {
	if !sameAgent([]byte("AGENT"), []byte("AGENT")) || sameAgent([]byte("AGENT"), []byte("OTHER")) {
		t.Error("two byte strings are the same agent exactly when they are equal")
	}
}

// Reading an initrd takes a while (lsinitrd lists it, and the agent in it is compared with the installed one),
// and a command that says nothing meanwhile looks hung. Each phase says what it is about to do.
func TestTheStaticChecksSayWhatTheyAreDoingAsTheyGo(t *testing.T) {
	fakeRehearsal(t, []string{"synctang", "network"}, nil, "AGENT", "AGENT", "ro quiet")
	idPath, confPath := systemFiles(t, "IDENTITY", "")
	was := rehearsalStep
	t.Cleanup(func() { rehearsalStep = was })
	var steps []string
	rehearsalStep = func(s string) { steps = append(steps, s) }

	staticRehearsalChecks(idPath, confPath)

	want := []string{"initrd", "dracut modules", "agent", "command line"}
	if len(steps) != len(want) {
		t.Fatalf("steps = %q", steps)
	}
	for i, w := range want {
		if !strings.Contains(steps[i], w) {
			t.Errorf("step %d = %q, want it to mention %q", i, steps[i], w)
		}
	}
}

// A volume whose key holders were all enrolled without a transport id: the agent refuses to answer anyone (it could
// not tell a key holder from a stranger), so waiting for an approval could never end well. It is said before the
// wait, as a volume with nobody enrolled is.
func newFixtureWithoutTransportIDs(t *testing.T) openFixture {
	t.Helper()
	passphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, passphrase)
	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatal(err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := enrolRecipient(device, passphrase, S, "the-machine", "MACHINE-ID", ""); err != nil {
		t.Fatal(err)
	}
	machineKey := filepath.Join(t.TempDir(), "machine.pem")
	if _, err := transport.LoadOrCreateCert(machineKey); err != nil {
		t.Fatal(err)
	}
	return openFixture{device: device, machineKey: machineKey, addr: freeTestAddr(t), s: s}
}

func TestRehearseSaysAtOnceWhenNoEnrolledKeyHolderHasATransportId(t *testing.T) {
	fakeOpener(t)
	f := newFixtureWithoutTransportIDs(t)
	withStaticChecks(t, nil)
	start := time.Now()
	r := waitFor(t, startOpen([]string{"rehearse", "--device", f.device, "--machine-key-file", f.machineKey, "--direct-addr", f.addr, "--timeout", "30s"}))
	if r.code != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("exit %d after %s:\n%s%s", r.code, time.Since(start), r.stdout, r.stderr)
	}
	for _, want := range []string{"none of them has a transport id", "--recipient-transport-id", "synctang-unlocker status"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.stderr)
		}
	}
	if strings.Contains(r.stdout, "waiting for a key holder") {
		t.Errorf("nobody should be asked to approve:\n%s", r.stdout)
	}
}

func TestOpenSaysAtOnceWhenNoEnrolledKeyHolderHasATransportId(t *testing.T) {
	fakeOpener(t)
	f := newFixtureWithoutTransportIDs(t)
	start := time.Now()
	r := waitFor(t, startOpen([]string{"open", "--machine-key-file", f.machineKey, "--direct-addr", f.addr, "--timeout", "30s", f.device}))
	if r.code != 1 || time.Since(start) > 5*time.Second || !strings.Contains(r.stderr, "none of them has a transport id") {
		t.Errorf("exit %d after %s:\n%s%s", r.code, time.Since(start), r.stdout, r.stderr)
	}
}
