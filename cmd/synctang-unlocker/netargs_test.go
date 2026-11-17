// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"strings"
	"testing"
)

func netArgs(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runNetworkArgs(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// What a person pastes into the boot loader: the line and nothing else, so that $(...) takes it as it is.
func TestNetworkArgsPrintsTheArgumentsAndNothingElseOnStandardOutput(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t, "nameserver 192.0.2.53\nnameserver 192.0.2.54\n")
	code, out, errOut := netArgs(t)
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if want := "ip=192.0.2.10::192.0.2.1:255.255.255.0::ens3:none nameserver=192.0.2.53 nameserver=192.0.2.54\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

// Nothing is needed on a DHCP network, and a script that pastes the output must get nothing, not a sentence.
func TestNetworkArgsSaysTheInitrdNeedsNothingOnADHCPNetworkAndPrintsNothing(t *testing.T) {
	dhcpHost().install(t)
	notWireless(t)
	code, out, errOut := netArgs(t)
	if code != 0 || out != "" || !strings.Contains(errOut, "DHCP") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestNetworkArgsFailsWithTheReasonWhenItCannotTell(t *testing.T) {
	hostNet{"-j -4 route show default": `[]`}.install(t)
	code, out, errOut := netArgs(t)
	if code != 1 || out != "" || !strings.Contains(errOut, "default route") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

// Several default routes are what the check cannot judge; naming the interface is the way out.
func TestNetworkArgsTakesTheInterfaceWhenSeveralHaveADefaultRoute(t *testing.T) {
	h := staticHost()
	h["-j -4 route show default"] = `[{"dst":"default","dev":"ens4","gateway":"198.51.100.1","protocol":"dhcp"},` + strings.Trim(staticRouteJSON, "[]") + `]`
	h["-j -d link show dev ens4"] = `[{"ifname":"ens4"}]`
	h.install(t)
	notWireless(t)
	resolvers(t)

	if code, _, errOut := netArgs(t); code != 1 || !strings.Contains(errOut, "--interface") {
		t.Errorf("without the flag it should say how to choose: exit %d, stderr %q", code, errOut)
	}
	code, out, errOut := netArgs(t, "--interface", "ens3")
	if code != 0 || errOut != "" || !strings.HasPrefix(out, "ip=192.0.2.10::192.0.2.1:255.255.255.0::ens3:none") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if code, out, errOut := netArgs(t, "--interface", "ens4"); code != 0 || out != "" || !strings.Contains(errOut, "DHCP") {
		t.Errorf("the DHCP interface needs nothing: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if code, _, errOut := netArgs(t, "--interface", "ens9"); code != 1 || !strings.Contains(errOut, "ens9") {
		t.Errorf("an interface with no default route: exit %d, stderr %q", code, errOut)
	}
}

func TestNetworkArgsTakesNoArguments(t *testing.T) {
	if code, _, errOut := netArgs(t, "ens3"); code != 2 || !strings.Contains(errOut, "ens3") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

// The way in is the check's own message, so that nobody has to remember the name.
func TestTheRehearsalProblemNamesTheCommandThatPrintsTheArguments(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t)
	if c := bootNetworkCheck("ro"); !strings.Contains(c.Detail, "synctang-unlocker network-args") {
		t.Errorf("%q", c.Detail)
	}
}
