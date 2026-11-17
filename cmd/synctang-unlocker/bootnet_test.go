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
	was, wasLoader, wasDefaults := ipOutput, bootLoaderConfigs, grubDefaults
	t.Cleanup(func() { ipOutput, bootLoaderConfigs, grubDefaults = was, wasLoader, wasDefaults })
	// A test that wants a boot loader says so with bootLoader; the machine the tests run on has its own.
	bootLoaderConfigs, grubDefaults = nil, nil
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

// bootLoader writes the files the boot loader's settings are in, by path under a temporary directory, and makes
// them the ones the check reads: the generated configuration and the defaults it is generated from.
func bootLoader(t *testing.T, generated, defaults map[string]string) {
	t.Helper()
	dir := t.TempDir()
	write := func(files map[string]string) (globs []string) {
		for name, content := range files {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			globs = append(globs, path)
		}
		return globs
	}
	bootLoaderConfigs, grubDefaults = write(generated), write(defaults)
}

const (
	// What update-grub writes on Debian and Ubuntu: the entry of each kernel, and its recovery entry.
	staticIP = "ip=192.0.2.10::192.0.2.1:255.255.255.0::ens3:none nameserver=192.0.2.53"
)

func grubCfg(args ...string) string {
	var b strings.Builder
	b.WriteString("set default=\"0\"\nmenuentry 'Ubuntu' --class ubuntu {\n\tlinux\t/boot/vmlinuz-7.0.0-43-generic root=UUID=1234 ro " + args[0] + "\n\tinitrd\t/boot/initrd.img-7.0.0-43-generic\n}\n")
	b.WriteString("submenu 'Advanced options for Ubuntu' {\n")
	b.WriteString("\tmenuentry 'Ubuntu, with Linux 7.0.0-30-generic' {\n\t\tlinux\t/boot/vmlinuz-7.0.0-30-generic root=UUID=1234 ro " + args[len(args)-1] + "\n\t}\n")
	b.WriteString("\tmenuentry 'Ubuntu, with Linux 7.0.0-43-generic (recovery mode)' {\n\t\tlinux\t/boot/vmlinuz-7.0.0-43-generic root=UUID=1234 ro recovery nomodeset\n\t}\n}\n")
	return b.String()
}

// The boot has not happened yet, so the settings of the next one are what can be looked at: the person who
// edited /etc/default/grub and ran update-grub did everything right, and must not be told otherwise.
func TestAStaticAddressInTheBootLoaderForTheNextBootIsFineAndSaysToReboot(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t, "nameserver 192.0.2.53\n")
	bootLoader(t, map[string]string{"grub/grub.cfg": grubCfg(staticIP+" quiet", staticIP+" quiet")}, nil)
	c := bootNetworkCheck("ro quiet")
	if c.Status != "ok" || !strings.Contains(c.Detail, "reboot") || !strings.Contains(c.Detail, "grub.cfg") {
		t.Errorf("%+v", c)
	}
}

// The recovery entry has no ip= when it was put in GRUB_CMDLINE_LINUX_DEFAULT, which recovery entries leave out;
// it is not what a machine boots into by itself.
func TestARecoveryEntryWithoutTheArgumentDoesNotCount(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t)
	bootLoader(t, map[string]string{"grub/grub.cfg": grubCfg(staticIP, staticIP)}, nil)
	if c := bootNetworkCheck("ro"); c.Status != "ok" {
		t.Errorf("%+v", c)
	}
}

// The case that matters: /etc/default/grub is edited and update-grub was not run, so GRUB has nothing.
func TestTheArgumentOnlyInTheGRUBDefaultsIsAProblemThatSaysToRunUpdateGrub(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t)
	bootLoader(t,
		map[string]string{"grub/grub.cfg": grubCfg("quiet", "quiet")},
		map[string]string{"default/grub": "GRUB_DEFAULT=0\nGRUB_CMDLINE_LINUX=\"" + staticIP + " rd.synctang.log=trace\"\n"})
	c := bootNetworkCheck("ro quiet")
	if c.Status != "problem" || !strings.Contains(c.Detail, "update-grub") || !strings.Contains(c.Detail, "grub.cfg") {
		t.Errorf("%+v", c)
	}
}

func TestNeitherPlaceHavingTheArgumentIsTheProblemWithTheLineToAdd(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t)
	bootLoader(t,
		map[string]string{"grub/grub.cfg": grubCfg("quiet", "quiet")},
		map[string]string{"default/grub": "GRUB_CMDLINE_LINUX=\"\"\n"})
	c := bootNetworkCheck("ro")
	if c.Status != "problem" || strings.Contains(c.Detail, "run update-grub") || !strings.Contains(c.Detail, "ip=192.0.2.10::192.0.2.1:255.255.255.0::ens3:none") {
		t.Errorf("%+v", c)
	}
}

func TestSomeEntriesWithoutTheArgumentAreAProblemThatSaysHowMany(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t)
	bootLoader(t, map[string]string{"grub/grub.cfg": grubCfg(staticIP, "quiet")}, nil)
	c := bootNetworkCheck("ro")
	if c.Status != "problem" || !strings.Contains(c.Detail, "1 of 2") {
		t.Errorf("%+v", c)
	}
}

// An address that was changed after the argument was written is the argument going stale.
func TestAnArgumentForAnotherAddressIsAProblemThatNamesBoth(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t)
	stale := "ip=198.51.100.7::198.51.100.1:255.255.255.0::ens3:none"
	bootLoader(t, map[string]string{"grub/grub.cfg": grubCfg(stale, stale)}, nil)
	c := bootNetworkCheck("ro")
	if c.Status != "problem" || !strings.Contains(c.Detail, "198.51.100.7") || !strings.Contains(c.Detail, "192.0.2.10") {
		t.Errorf("%+v", c)
	}
}

// Fedora and systemd-boot keep one file per entry, with the arguments on an "options" line.
func TestBootLoaderEntriesWithAnOptionsLineAreReadToo(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t)
	entry := func(args string) string {
		return "title Fedora\nlinux /vmlinuz-6.12\ninitrd /initramfs-6.12.img\noptions root=UUID=1 ro " + args + "\n"
	}
	bootLoader(t, map[string]string{"entries/a.conf": entry(staticIP), "entries/b.conf": entry(staticIP)}, nil)
	if c := bootNetworkCheck("ro"); c.Status != "ok" {
		t.Errorf("%+v", c)
	}
	bootLoader(t, map[string]string{"entries/a.conf": entry(staticIP), "entries/b.conf": entry("quiet")}, nil)
	if c := bootNetworkCheck("ro"); c.Status != "problem" || !strings.Contains(c.Detail, "1 of 2") {
		t.Errorf("%+v", c)
	}
}

// Words that only end in ip= are other arguments.
func TestOnlyAWholeIPArgumentCounts(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t)
	other := "rd.ip=192.0.2.10::192.0.2.1:255.255.255.0::ens3:none xip=192.0.2.10:"
	bootLoader(t, map[string]string{"grub/grub.cfg": grubCfg(other, other)}, nil)
	if c := bootNetworkCheck("ro"); c.Status != "problem" {
		t.Errorf("%+v", c)
	}
}

// Where nothing of the boot loader can be read, the check is what it was: the line to add.
func TestWithNoBootLoaderConfigurationFoundTheProblemIsTheLineToAdd(t *testing.T) {
	staticHost().install(t)
	notWireless(t)
	resolvers(t)
	if c := bootNetworkCheck("ro"); c.Status != "problem" || !strings.Contains(c.Detail, "ip=192.0.2.10") {
		t.Errorf("%+v", c)
	}
}
