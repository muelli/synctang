// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"strings"
	"testing"
)

func stubPrivilege(t *testing.T, haveRun0 bool, args ...string) {
	t.Helper()
	oldLook, oldArgs := lookPath, commandLine
	t.Cleanup(func() { lookPath, commandLine = oldLook, oldArgs })
	lookPath = func(name string) (string, error) {
		if name == "run0" && haveRun0 {
			return "/usr/bin/run0", nil
		}
		return "", errors.New("not found")
	}
	commandLine = func() []string { return args }
}

// Reading a LUKS header takes root. Where it was not run as root, the message says so, with the command to run
// it as root: run0 where the system has it (systemd 256 and later), sudo otherwise.
func TestTheRootHintNamesRun0WhereThereIsOneAndSudoWhereThereIsNot(t *testing.T) {
	stubPrivilege(t, true, "/usr/sbin/synctang-unlocker", "status")
	if got := asRoot(); got != "run0 synctang-unlocker status" {
		t.Errorf("%q", got)
	}
	stubPrivilege(t, false, "/usr/sbin/synctang-unlocker", "status")
	if got := asRoot(); got != "sudo synctang-unlocker status" {
		t.Errorf("%q", got)
	}
}

func TestTheRootHintKeepsTheArgumentsAndQuotesWhatNeedsIt(t *testing.T) {
	stubPrivilege(t, false, "synctang-unlocker", "pair", "--device", "/dev/vda3", "--name", "my machine")
	if got := asRoot(); got != "sudo synctang-unlocker pair --device /dev/vda3 --name 'my machine'" {
		t.Errorf("%q", got)
	}
}

func TestWithNoBlockDeviceFoundAndNotRootTheMessageSaysRootAndHowToGetIt(t *testing.T) {
	stubPrivilege(t, true, "synctang-unlocker", "status")
	err := noDeviceError(false)
	for _, want := range []string{"takes root", "run0 synctang-unlocker status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("no %q in %q", want, err)
		}
	}
	// As root, nothing is wrong with the privileges, and the message is the plain one.
	if got := noDeviceError(true).Error(); got != "no block device carries an mr-1 token" {
		t.Errorf("%q", got)
	}
}

func TestAFailedHeaderReadSaysRootOnlyWhenPermissionWasTheReason(t *testing.T) {
	stubPrivilege(t, false, "synctang-unlocker", "status", "--device", "/dev/vda3")
	was := isRoot
	t.Cleanup(func() { isRoot = was })
	isRoot = func() bool { return false }
	denied := errors.New("cryptsetup luksDump /dev/vda3: exit status 1: Cannot open device /dev/vda3, permission denied.")
	if got := permissionHint(denied); !strings.Contains(got, "takes root") || !strings.Contains(got, "sudo synctang-unlocker status --device /dev/vda3") {
		t.Errorf("%q", got)
	}
	if got := permissionHint(errors.New("Device /dev/vda3 is not a valid LUKS device.")); got != "" {
		t.Errorf("a hint for a failure that was not about permission: %q", got)
	}
	isRoot = func() bool { return true }
	if got := permissionHint(denied); got != "" {
		t.Errorf("a hint for root: %q", got)
	}
	if permissionHint(nil) != "" {
		t.Error("a hint for no error")
	}
}
