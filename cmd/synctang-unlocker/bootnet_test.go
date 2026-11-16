// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	dhcpRouteJSON = `[{"dst":"default","gateway":"192.168.118.1","dev":"enp1s0","protocol":"dhcp","prefsrc":"192.168.118.114","metric":100,"flags":[]}]`
	dhcpAddrJSON  = `[{"ifname":"enp1s0","addr_info":[{"family":"inet","local":"192.168.118.114","prefixlen":24,"scope":"global","dynamic":true,"valid_life_time":635544}]}]`

	staticRouteJSON = `[{"dst":"default","gateway":"192.0.2.1","dev":"ens3","protocol":"static","prefsrc":"192.0.2.10","flags":[]}]`
	staticAddrJSON  = `[{"ifname":"ens3","addr_info":[{"family":"inet","local":"192.0.2.10","prefixlen":24,"scope":"global","valid_life_time":4294967295}]}]`
	plainLinkJSON   = `[{"ifname":"ens3","operstate":"UP"}]`
)

// hostNet says what "ip -j" prints for each question the check asks, by the arguments joined with
// spaces; a question not in the map is an error, so the check cannot ask more than the test knows.
type hostNet map[string]string

func (h hostNet) install(t *testing.T) {
	t.Helper()
	was := ipOutput
	t.Cleanup(func() { ipOutput = was })
	ipOutput = func(args ...string) ([]byte, error) {
		if out, ok := h[strings.Join(args, " ")]; ok {
			return []byte(out), nil
		}
		return nil, errors.New("unexpected question: ip " + strings.Join(args, " "))
	}
}

func staticHost() hostNet {
	return hostNet{
		"-j -4 route show default": staticRouteJSON,
		"-j -4 addr show dev ens3": staticAddrJSON,
		"-j -d link show dev ens3": plainLinkJSON,
	}
}

func dhcpHost() hostNet {
	return hostNet{
		"-j -4 route show default":   dhcpRouteJSON,
		"-j -4 addr show dev enp1s0": dhcpAddrJSON,
		"-j -d link show dev enp1s0": `[{"ifname":"enp1s0"}]`,
	}
}

// resolvers makes the files the check reads the DNS servers from, in order.
func resolvers(t *testing.T, contents ...string) {
	t.Helper()
	was := upstreamResolvConfs
	t.Cleanup(func() { upstreamResolvConfs = was })
	upstreamResolvConfs = nil
	for i, content := range contents {
		path := filepath.Join(t.TempDir(), "resolv.conf"+string(rune('a'+i)))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		upstreamResolvConfs = append(upstreamResolvConfs, path)
	}
}

func notWireless(t *testing.T) {
	t.Helper()
	was := isWireless
	t.Cleanup(func() { isWireless = was })
	isWireless = func(string) bool { return false }
}

func TestADHCPNetworkIsFineBecauseTheInitrdAsksForDHCPToo(t *testing.T) {
	dhcpHost().install(t)
	notWireless(t)
	if c := bootNetworkCheck("ro quiet"); c.Status != "ok" || c.Detail != "" {
		t.Errorf("%+v", c)
	}
}

func TestAnAddressSetByHandIsAProblemWithTheKernelArgumentThatFixesIt(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t, "nameserver 192.0.2.53\n")
	c := bootNetworkCheck("ro quiet")
	if c.Status != "problem" {
		t.Fatalf("%+v", c)
	}
	for _, want := range []string{
		"ip=192.0.2.10::192.0.2.1:255.255.255.0::ens3:none nameserver=192.0.2.53",
		"DHCP",
		"kernel command line",
	} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("the problem should say %q: %q", want, c.Detail)
		}
	}
}

// ifupdown's "static" stanza leaves its routes with protocol "boot", which is not DHCP either.
func TestARouteThatIsNotFromDHCPIsTakenForStaticWhateverItsProtocolIsCalled(t *testing.T) {
	for _, protocol := range []string{"boot", "static", "kernel", "", "bird"} {
		h := staticHost()
		h["-j -4 route show default"] = strings.Replace(staticRouteJSON, `"protocol":"static",`, `"protocol":"`+protocol+`",`, 1)
		if protocol == "" {
			h["-j -4 route show default"] = strings.Replace(staticRouteJSON, `"protocol":"static",`, "", 1)
		}
		h.install(t)
		notWireless(t)
		resolvers(t)
		if c := bootNetworkCheck("ro"); c.Status != "problem" {
			t.Errorf("protocol %q: %+v", protocol, c)
		}
	}
}

func TestTheDNSServersOfTheNetworkGoIntoTheArgumentAndTheLocalStubDoesNot(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	// The first file is what systemd-resolved keeps of the real servers; the stub in /etc is no use in the initrd.
	resolvers(t, "# comment\nnameserver 192.0.2.53\nnameserver 2001:db8::53\nnameserver 192.0.2.54\nnameserver 192.0.2.55\n", "nameserver 127.0.0.53\nnameserver 192.0.2.99\n")
	c := bootNetworkCheck("ro")
	if !strings.Contains(c.Detail, " nameserver=192.0.2.53 nameserver=192.0.2.54") {
		t.Errorf("the first two IPv4 servers of the first file that has any: %q", c.Detail)
	}
	if strings.Contains(c.Detail, "127.0.0.53") || strings.Contains(c.Detail, "192.0.2.55") || strings.Contains(c.Detail, "192.0.2.99") {
		t.Errorf("a stub, a third server and a server of a later file are not wanted: %q", c.Detail)
	}
}

func TestWithNoDNSServerFoundTheArgumentHasNoneAndTheAgentUsesItsOwnFallback(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t, "nameserver 127.0.0.53\n")
	c := bootNetworkCheck("ro")
	if c.Status != "problem" || !strings.Contains(c.Detail, "ip=192.0.2.10::192.0.2.1:255.255.255.0::ens3:none") || strings.Contains(c.Detail, "nameserver=") {
		t.Errorf("%+v", c)
	}
}

func TestAnIPArgumentOnTheKernelCommandLineIsNotSecondGuessed(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	c := bootNetworkCheck("ro ip=192.0.2.10::192.0.2.1:255.255.255.0::ens3:none quiet")
	if c.Status != "unchecked" || !strings.Contains(c.Detail, "ip=") {
		t.Errorf("%+v", c)
	}
}

func TestWhatTheCheckCannotJudgeIsUncheckedAndSaysWhy(t *testing.T) {
	two := staticHost()
	two["-j -4 route show default"] = `[` + strings.Trim(staticRouteJSON, "[]") + `,{"dst":"default","gateway":"198.51.100.1","dev":"ens4","protocol":"dhcp"}]`
	onlink := staticHost()
	onlink["-j -4 route show default"] = `[{"dst":"default","dev":"ens3","protocol":"static"}]`
	bridge := staticHost()
	bridge["-j -d link show dev ens3"] = `[{"ifname":"ens3","linkinfo":{"info_kind":"bridge"}}]`
	noAddr := staticHost()
	noAddr["-j -4 addr show dev ens3"] = `[{"ifname":"ens3","addr_info":[]}]`
	for name, tc := range map[string]struct {
		host hostNet
		want string
	}{
		"no default route":    {hostNet{"-j -4 route show default": `[]`}, "default route"},
		"two interfaces":      {two, "more than one"},
		"gateway on the link": {onlink, "gateway"},
		"a bridge":            {bridge, "bridge"},
		"no address found":    {noAddr, "address"},
		"ip does not answer":  {hostNet{}, "ip"},
		"ip prints no JSON":   {hostNet{"-j -4 route show default": "not json"}, "ip"},
	} {
		tc.host.install(t)
		notWireless(t)
		resolvers(t)
		c := bootNetworkCheck("ro")
		if c.Status != "unchecked" || !strings.Contains(c.Detail, tc.want) {
			t.Errorf("%s: %+v", name, c)
		}
	}
}

func TestAWirelessInterfaceIsUncheckedBecauseTheInitrdCannotJoinANetwork(t *testing.T) {
	staticHost().install(t)
	was := isWireless
	t.Cleanup(func() { isWireless = was })
	isWireless = func(dev string) bool { return dev == "ens3" }
	if c := bootNetworkCheck("ro"); c.Status != "unchecked" || !strings.Contains(c.Detail, "wireless") {
		t.Errorf("%+v", c)
	}
}

func TestOnlyTheFirstDefaultRouteDevicesMatter(t *testing.T) {
	// Two default routes on the one interface (two metrics) are one interface.
	h := staticHost()
	h["-j -4 route show default"] = `[` + strings.Trim(staticRouteJSON, "[]") + `,{"dst":"default","gateway":"192.0.2.2","dev":"ens3","protocol":"static","metric":200}]`
	h.install(t)
	notWireless(t)
	resolvers(t)
	if c := bootNetworkCheck("ro"); c.Status != "problem" {
		t.Errorf("%+v", c)
	}
}

// The argument is only worth printing if the initrd's own tool takes it: systemd-network-generator is
// what dracut runs in the initrd to turn ip= into a network file, so run it on what the check prints.
func TestTheArgumentThePrintedLineHasIsOneTheInitrdsGeneratorTurnsIntoAStaticNetwork(t *testing.T) {
	generator := "/usr/lib/systemd/systemd-network-generator"
	if _, err := os.Stat(generator); err != nil {
		t.Skip("no systemd-network-generator here")
	}
	staticHost().install(t)
	notWireless(t)
	resolvers(t, "nameserver 192.0.2.53\n")
	detail := bootNetworkCheck("ro").Detail
	var args []string
	for _, f := range strings.Fields(detail) {
		if strings.HasPrefix(f, "ip=") || strings.HasPrefix(f, "nameserver=") {
			args = append(args, strings.TrimRight(f, ".,;"))
		}
	}
	if len(args) != 2 {
		t.Fatalf("want an ip= and a nameserver= in %q", detail)
	}
	root := t.TempDir()
	if out, err := exec.Command(generator, append([]string{"--root=" + root, "--"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("the generator refused %q: %v: %s", args, err, out)
	}
	files, _ := filepath.Glob(filepath.Join(root, "run/systemd/network/*.network"))
	if len(files) != 1 {
		t.Fatalf("network files = %v", files)
	}
	got, _ := os.ReadFile(files[0])
	for _, want := range []string{"Name=ens3", "DHCP=no", "DNS=192.0.2.53", "Address=192.0.2.10/24", "Gateway=192.0.2.1"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the network file lacks %q:\n%s", want, got)
		}
	}
}
