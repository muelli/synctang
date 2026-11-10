// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"github.com/muelli/synctang/internal/ui"
	"io"
	"os"
	"os/exec"
	"strings"
)

// What the initrd carries for synctang, by the names it has inside the image. The agent
// runs in the initrd with nothing else to read: its machine identity, and the relay and
// discovery servers it is to use. An identity that is missing there is made afresh at every
// boot, and no key holder paired against the old one can find the machine; this is the
// state "pair" and "enrol" can leave behind on a machine whose initrd was built before the
// identity existed.
const (
	initrdIdentity    = "etc/synctang/machine.pem"
	initrdNetworkConf = "etc/synctang/network.conf"
)

// The two ways of looking into an initrd: its listing, and the contents of one file. Both
// are lsinitrd's, which reads the image of the running kernel; variables so that tests can
// say what is in it.
var (
	initrdListing = func() ([]string, error) {
		out, err := runCommand(nil, "lsinitrd")
		if err != nil {
			return nil, err
		}
		var names []string
		for _, line := range strings.Split(string(out), "\n") {
			// "-rw-------   1 root  root   773 Nov  3 22:18 etc/synctang/machine.pem", and
			// "path -> target" for a link.
			fields := strings.Fields(line)
			if len(fields) >= 9 && strings.ContainsAny(fields[0][:1], "-dl") {
				names = append(names, fields[8])
			}
		}
		return names, nil
	}
	initrdContent = func(name string) ([]byte, error) {
		return runCommand(nil, "lsinitrd", "-f", name)
	}

	// rebuildInitrd runs dracut over the initrd of the running kernel.
	rebuildInitrd = func(stdout, stderr io.Writer) error {
		cmd := exec.Command(resolveCommand("dracut"), "--force")
		cmd.Stdout, cmd.Stderr = stdout, stderr
		return cmd.Run()
	}
)

// initrdStatus is what comparing the initrd with the system found. Checked is false when the
// initrd could not be looked into at all, with the Reason; otherwise each Problem completes
// the sentence "the initrd ...".
type initrdStatus struct {
	Checked  bool
	Reason   string
	Problems []string
}

func sameBytes(a, b []byte) bool {
	x, y := sha256.Sum256(a), sha256.Sum256(b)
	return bytes.Equal(x[:], y[:])
}

// checkInitrd compares what the initrd carries with the identity and the network.conf on the
// system. By hash and in memory: nothing of the identity is written anywhere or printed.
func checkInitrd(identityPath, networkConfPath string) initrdStatus {
	identity, idErr := os.ReadFile(identityPath)
	conf, confErr := os.ReadFile(networkConfPath)
	if idErr != nil && confErr != nil {
		// Nothing exists yet that an initrd would have to carry.
		return initrdStatus{Checked: true}
	}
	names, err := initrdListing()
	if err != nil {
		return initrdStatus{Reason: err.Error()}
	}
	has := map[string]bool{}
	for _, n := range names {
		has[n] = true
	}

	st := initrdStatus{Checked: true}
	compare := func(name, path string, system []byte, sysErr error, problems func(inInitrd, differs bool) string) bool {
		switch {
		case sysErr != nil && !has[name]:
			return true
		case sysErr != nil:
			st.Problems = append(st.Problems, problems(true, false))
		case !has[name]:
			st.Problems = append(st.Problems, problems(false, false))
		default:
			inside, err := initrdContent(name)
			if err != nil {
				st.Checked, st.Reason = false, err.Error()
				return false
			}
			if !sameBytes(inside, system) {
				st.Problems = append(st.Problems, problems(true, true))
			}
		}
		return true
	}
	if !compare(initrdIdentity, identityPath, identity, idErr, func(inInitrd, differs bool) string {
		if differs {
			return fmt.Sprintf("carries a different machine identity from %s, so a key holder paired now would look for a machine that the next boot does not announce", identityPath)
		}
		return fmt.Sprintf("does not contain this machine's identity (%s), so at boot the agent makes a new one that no key holder knows, and none can find this machine", identityPath)
	}) {
		return st
	}
	if !compare(initrdNetworkConf, networkConfPath, conf, confErr, func(inInitrd, differs bool) string {
		switch {
		case differs:
			return fmt.Sprintf("carries a different network.conf from the current %s, so at boot the agent would use the older relay and discovery settings", networkConfPath)
		case inInitrd:
			return fmt.Sprintf("still carries a network.conf that no longer exists (%s), so at boot the agent would use servers that are no longer configured", networkConfPath)
		}
		return fmt.Sprintf("does not contain the current %s, so at boot the agent would not use the relay and discovery servers it names", networkConfPath)
	}) {
		return st
	}
	return st
}

// printInitrdStatus writes the initrd's state as "status" shows it.
func printInitrdStatus(w io.Writer, identityPath, networkConfPath string) {
	st := checkInitrd(identityPath, networkConfPath)
	switch {
	case !st.Checked:
		fmt.Fprintf(w, "initrd: could not be checked (%s)\n", st.Reason)
	case len(st.Problems) == 0:
		if _, err := os.Stat(identityPath); err != nil {
			fmt.Fprintln(w, "initrd: nothing to carry yet, this machine has no identity")
			return
		}
		fmt.Fprintln(w, "initrd: carries this machine's identity"+map[bool]string{true: " and the current network.conf", false: ""}[fileExists(networkConfPath)])
	default:
		for _, p := range st.Problems {
			fmt.Fprintf(w, "initrd: %s\n", p)
		}
		fmt.Fprintln(w, "initrd: fix it with: dracut --force (as root; it rebuilds the initrd of the running kernel)")
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// initrdOptions says what to check after an enrolment, and whether to rebuild if it is wrong.
type initrdOptions struct {
	Identity    string
	NetworkConf string
	Rebuild     bool
}

// afterEnrolmentFor is afterEnrolment for the identity file a command was given. The initrd
// only ever carries /etc/synctang/machine.pem, so for any other identity file (a test, an
// image prepared for another machine) there is nothing to say.
func afterEnrolmentFor(stdout, stderr io.Writer, identityPath, networkConfPath string, rebuild bool) int {
	if identityPath != defaultMachineKeyFile {
		return 0
	}
	return afterEnrolment(stdout, stderr, initrdOptions{Identity: identityPath, NetworkConf: networkConfPath, Rebuild: rebuild})
}

// afterEnrolment looks at the initrd once a key holder is enrolled, because that is when a
// machine that has just made its identity has an initrd that cannot carry it. A wrong initrd
// is said so, in plain words, with how to put it right; with Rebuild it is put right here.
// The result is the exit status: a run that was asked to leave the initrd right and did not
// has failed, though the key holder is enrolled.
func afterEnrolment(stdout, stderr io.Writer, o initrdOptions) int {
	st := checkInitrd(o.Identity, o.NetworkConf)
	if st.Checked && len(st.Problems) == 0 {
		return 0
	}
	if !st.Checked && !o.Rebuild {
		ui.Errorf(stderr, "could not check whether the initrd carries this machine's identity (%s); "+
			"after the first pairing run dracut --force, or this machine may not be found at boot\n", st.Reason)
		return 0
	}
	if st.Checked {
		for _, p := range st.Problems {
			ui.Notef(stderr, "warning: the initrd %s\n", p)
		}
		if !o.Rebuild {
			ui.Errorln(stderr, "fix it with: dracut --force (as root), or repeat this command with --rebuild-initrd")
			return 0
		}
	}

	fmt.Fprintln(stdout, "rebuilding the initrd: dracut --force")
	if err := rebuildInitrd(stdout, stderr); err != nil {
		ui.Errorf(stderr, "the key holder was enrolled, but the initrd could not be rebuilt: %v; run dracut --force yourself\n", err)
		return 1
	}
	after := checkInitrd(o.Identity, o.NetworkConf)
	if after.Checked && len(after.Problems) == 0 {
		printInitrdStatus(stdout, o.Identity, o.NetworkConf)
		return 0
	}
	reason := after.Reason
	if after.Checked {
		reason = strings.Join(after.Problems, "; ")
	}
	ui.Errorf(stderr, "the key holder was enrolled and dracut ran, but the initrd still does not match: %s\n", reason)
	return 1
}
