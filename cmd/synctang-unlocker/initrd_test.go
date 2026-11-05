// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

// fakeInitrd stands in for lsinitrd: the files the initrd holds, or an error for the whole
// image (no lsinitrd, no image, no permission to read it).
func fakeInitrd(t *testing.T, files map[string]string, err error) {
	t.Helper()
	wasList, wasFile := initrdListing, initrdContent
	t.Cleanup(func() { initrdListing, initrdContent = wasList, wasFile })
	initrdListing = func() ([]string, error) {
		if err != nil {
			return nil, err
		}
		var names []string
		for name := range files {
			names = append(names, name)
		}
		return names, nil
	}
	initrdContent = func(name string) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		content, ok := files[name]
		if !ok {
			return nil, errors.New("not in the image")
		}
		return []byte(content), nil
	}
}

func systemFiles(t *testing.T, identity, networkConf string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	idPath, confPath := filepath.Join(dir, "machine.pem"), filepath.Join(dir, "network.conf")
	if identity != "" {
		if err := os.WriteFile(idPath, []byte(identity), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if networkConf != "" {
		if err := os.WriteFile(confPath, []byte(networkConf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return idPath, confPath
}

func TestInitrdThatCarriesTheIdentityIsFine(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeInitrd(t, map[string]string{"etc/synctang/machine.pem": "IDENTITY", "usr/bin/sh": "x"}, nil)
	st := checkInitrd(id, conf)
	if !st.Checked || len(st.Problems) != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestInitrdWithoutTheIdentityIsReportedInPlainWords(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeInitrd(t, map[string]string{"usr/bin/sh": "x"}, nil)
	st := checkInitrd(id, conf)
	if !st.Checked || len(st.Problems) != 1 || !strings.Contains(st.Problems[0], "does not contain") ||
		!strings.Contains(st.Problems[0], "new one") {
		t.Fatalf("%+v", st)
	}
}

func TestInitrdWithAnotherIdentityIsReported(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeInitrd(t, map[string]string{"etc/synctang/machine.pem": "SOMETHING ELSE"}, nil)
	st := checkInitrd(id, conf)
	if len(st.Problems) != 1 || !strings.Contains(st.Problems[0], "different") {
		t.Fatalf("%+v", st)
	}
	for _, p := range st.Problems {
		if strings.Contains(p, "IDENTITY") || strings.Contains(p, "SOMETHING") {
			t.Fatalf("a message must never carry the contents of the identity: %q", p)
		}
	}
}

func TestNothingToCompareBeforeTheIdentityExists(t *testing.T) {
	id, conf := systemFiles(t, "", "")
	fakeInitrd(t, map[string]string{}, nil)
	if st := checkInitrd(id, conf); !st.Checked || len(st.Problems) != 0 {
		t.Fatalf("no identity yet is not a problem: %+v", st)
	}
}

func TestNetworkConfThatTheInitrdLacksOrHasChanged(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "discovery=https://10.0.0.1:8443/v2/\n")
	fakeInitrd(t, map[string]string{"etc/synctang/machine.pem": "IDENTITY"}, nil)
	if st := checkInitrd(id, conf); len(st.Problems) != 1 || !strings.Contains(st.Problems[0], "network.conf") {
		t.Fatalf("a network.conf the initrd does not have: %+v", st)
	}
	fakeInitrd(t, map[string]string{"etc/synctang/machine.pem": "IDENTITY", "etc/synctang/network.conf": "old\n"}, nil)
	if st := checkInitrd(id, conf); len(st.Problems) != 1 || !strings.Contains(st.Problems[0], "different") {
		t.Fatalf("an older network.conf: %+v", st)
	}
	fakeInitrd(t, map[string]string{"etc/synctang/machine.pem": "IDENTITY", "etc/synctang/network.conf": "discovery=https://10.0.0.1:8443/v2/\n"}, nil)
	if st := checkInitrd(id, conf); len(st.Problems) != 0 {
		t.Fatalf("the same network.conf: %+v", st)
	}
}

func TestNetworkConfThatNoLongerExistsButIsStillInTheInitrd(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeInitrd(t, map[string]string{"etc/synctang/machine.pem": "IDENTITY", "etc/synctang/network.conf": "discovery=x\n"}, nil)
	if st := checkInitrd(id, conf); len(st.Problems) != 1 || !strings.Contains(st.Problems[0], "no longer") {
		t.Fatalf("%+v", st)
	}
}

func TestAnInitrdThatCannotBeReadIsSaidToBeUnchecked(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeInitrd(t, nil, errors.New("lsinitrd: command not found"))
	st := checkInitrd(id, conf)
	if st.Checked || !strings.Contains(st.Reason, "lsinitrd") || len(st.Problems) != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestStatusSaysWhatTheInitrdHas(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	for _, tc := range []struct {
		files map[string]string
		err   error
		want  string
	}{
		{map[string]string{"etc/synctang/machine.pem": "IDENTITY"}, nil, "initrd: carries this machine's identity"},
		{map[string]string{}, nil, "initrd: does not contain this machine's identity"},
		{nil, errors.New("permission denied"), "initrd: could not be checked (permission denied)"},
	} {
		fakeInitrd(t, tc.files, tc.err)
		var out bytes.Buffer
		printInitrdStatus(&out, id, conf)
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("want %q in:\n%s", tc.want, out.String())
		}
	}
	fakeInitrd(t, map[string]string{}, nil)
	var out bytes.Buffer
	printInitrdStatus(&out, id, conf)
	if !strings.Contains(out.String(), "dracut --force") {
		t.Errorf("a problem comes with the command that fixes it:\n%s", out.String())
	}
}

func fakeRebuild(t *testing.T, onRun func(), err error) *int {
	t.Helper()
	was := rebuildInitrd
	t.Cleanup(func() { rebuildInitrd = was })
	calls := 0
	rebuildInitrd = func(stdout, stderr io.Writer) error {
		calls++
		if onRun != nil {
			onRun()
		}
		return err
	}
	return &calls
}

func TestAfterEnrolmentWarnsAndTellsHowToFixIt(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeInitrd(t, map[string]string{}, nil)
	calls := fakeRebuild(t, nil, nil)
	var out, errOut bytes.Buffer
	code := afterEnrolment(&out, &errOut, initrdOptions{Identity: id, NetworkConf: conf})
	if code != 0 || *calls != 0 {
		t.Fatalf("exit %d, rebuilt %d times: a warning does not rebuild", code, *calls)
	}
	all := out.String() + errOut.String()
	if !strings.Contains(all, "does not contain") || !strings.Contains(all, "dracut --force") || !strings.Contains(all, "--rebuild-initrd") {
		t.Errorf("the warning should say what is wrong and both ways to fix it:\n%s", all)
	}
}

func TestAfterEnrolmentSaysNothingWhenTheInitrdIsRight(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeInitrd(t, map[string]string{"etc/synctang/machine.pem": "IDENTITY"}, nil)
	calls := fakeRebuild(t, nil, nil)
	var out, errOut bytes.Buffer
	if code := afterEnrolment(&out, &errOut, initrdOptions{Identity: id, NetworkConf: conf, Rebuild: true}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out.Len()+errOut.Len() != 0 || *calls != 0 {
		t.Errorf("a right initrd needs no words and no rebuild: out %q err %q, rebuilt %d", out.String(), errOut.String(), *calls)
	}
}

func TestRebuildInitrdRunsDracutAndChecksAgain(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	files := map[string]string{}
	fakeInitrd(t, files, nil)
	calls := fakeRebuild(t, func() { files["etc/synctang/machine.pem"] = "IDENTITY" }, nil)
	var out, errOut bytes.Buffer
	if code := afterEnrolment(&out, &errOut, initrdOptions{Identity: id, NetworkConf: conf, Rebuild: true}); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), errOut.String())
	}
	if *calls != 1 || !strings.Contains(out.String(), "carries this machine's identity") {
		t.Errorf("rebuilt %d times, output:\n%s", *calls, out.String())
	}
}

func TestAFailedRebuildIsAFailureButTheEnrolmentStands(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeInitrd(t, map[string]string{}, nil)
	fakeRebuild(t, nil, errors.New("dracut: exit status 1"))
	var out, errOut bytes.Buffer
	code := afterEnrolment(&out, &errOut, initrdOptions{Identity: id, NetworkConf: conf, Rebuild: true})
	if code != 1 || !strings.Contains(errOut.String(), "was enrolled") || !strings.Contains(errOut.String(), "dracut: exit status 1") {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut.String())
	}
}

func TestRebuildThatLeavesTheInitrdWrongIsReported(t *testing.T) {
	id, conf := systemFiles(t, "IDENTITY", "")
	fakeInitrd(t, map[string]string{}, nil)
	fakeRebuild(t, nil, nil) // "succeeds" but the identity is still not in the image
	var out, errOut bytes.Buffer
	if code := afterEnrolment(&out, &errOut, initrdOptions{Identity: id, NetworkConf: conf, Rebuild: true}); code != 1 {
		t.Fatalf("exit %d: the initrd is still wrong, so the run did not do what was asked", code)
	}
}

func TestACustomIdentityFileIsNotTheInitrdsBusiness(t *testing.T) {
	fakeInitrd(t, map[string]string{}, nil)
	calls := fakeRebuild(t, nil, nil)
	id, conf := systemFiles(t, "IDENTITY", "")
	var out, errOut bytes.Buffer
	code := afterEnrolmentFor(&out, &errOut, id /* not the default path */, conf, true)
	if code != 0 || out.Len()+errOut.Len() != 0 || *calls != 0 {
		t.Errorf("exit %d, output %q %q, rebuilt %d: the initrd only ever carries the default path", code, out.String(), errOut.String(), *calls)
	}
}

func enrolArgs(t *testing.T, extra ...string) ([]string, string) {
	t.Helper()
	pass := []byte("initrd-test-passphrase")
	device := formatLoopbackImage(t, pass)
	dir := t.TempDir()
	ppFile := filepath.Join(dir, "pass")
	if err := os.WriteFile(ppFile, pass, 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := mrcore.P256().RandomScalar()
	S, _ := mrcore.P256().ScalarBaseMult(s)
	args := append([]string{"enrol", "--device", device, "--pubkey", hex.EncodeToString(S),
		"--existing-passphrase-file", ppFile, "--machine-key-file", filepath.Join(dir, "machine.pem")}, extra...)
	return args, device
}

func TestEnrolWithACustomIdentityFileSaysNothingAboutTheInitrd(t *testing.T) {
	fakeInitrd(t, map[string]string{}, nil)
	calls := fakeRebuild(t, nil, nil)
	args, _ := enrolArgs(t, "--rebuild-initrd")
	var out, errOut bytes.Buffer
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if strings.Contains(out.String()+errOut.String(), "initrd") || *calls != 0 {
		t.Errorf("the initrd only carries the default identity path, so a custom one is none of its business (rebuilt %d):\n%s%s",
			*calls, out.String(), errOut.String())
	}
}

func TestStatusWithACustomIdentityFileDoesNotTalkAboutTheInitrd(t *testing.T) {
	fakeInitrd(t, map[string]string{}, nil)
	args, device := enrolArgs(t)
	var out, errOut bytes.Buffer
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("enrol: exit %d: %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	keyFile := args[len(args)-1]
	if code := run([]string{"status", "--device", device, "--machine-key-file", keyFile}, &out, &errOut); code != 0 {
		t.Fatalf("status: exit %d: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "initrd") {
		t.Errorf("status:\n%s", out.String())
	}
}
