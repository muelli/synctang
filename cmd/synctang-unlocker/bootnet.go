// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ipSearchPath is where ip(8) is when the caller's PATH does not have it: sudo and cron give root a short one.
var ipSearchPath = []string{"/usr/sbin/ip", "/sbin/ip", "/usr/bin/ip", "/bin/ip"}

// ipProgram is ip(8) on the caller's PATH, else in the places distributions put it, so that a PATH that was
// changed under a root shell is not what decides which program runs.
func ipProgram() string {
	if path, err := exec.LookPath("ip"); err == nil {
		return path
	}
	for _, path := range ipSearchPath {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "ip"
}

// The initrd asks for DHCP on every interface and does nothing else, unless the kernel command line
// has an ip= argument (dracut's, which systemd-network-generator turns into a network file). A
// machine whose address is set by hand would boot into the initrd with only an IPv6 link-local
// address, wait for a lease that nobody gives, and the agent would find no network.
//
// The running system is the only place that says how this machine gets its address, and every
// network manager (netplan, systemd-networkd, NetworkManager, ifupdown) ends by telling the kernel,
// which records where a route came from: "dhcp" for a lease, anything else for an address set by
// hand. So the check asks the kernel with ip(8) and does not read any manager's files.

// What the network check reads, as variables so that tests can say what is there.
var (
	// ipOutput runs ip(8) and returns what it prints.
	ipOutput = func(args ...string) ([]byte, error) {
		return exec.Command(ipProgram(), args...).Output() // i18n:skip a command, not a message
	}
	// upstreamResolvConfs are the files that list the DNS servers of the network itself, best first: the
	// stub resolver in /etc/resolv.conf answers on a local address that the initrd does not have.
	upstreamResolvConfs = []string{"/run/systemd/resolve/resolv.conf", "/run/NetworkManager/resolv.conf", "/etc/resolv.conf"}
	// bootLoaderConfigs are the files the next boot's kernel command lines are in: the configuration GRUB
	// generated (update-grub, grub2-mkconfig) and one file per entry for Fedora and systemd-boot. They are globs.
	bootLoaderConfigs = []string{"/boot/grub/grub.cfg", "/boot/grub2/grub.cfg", "/boot/loader/entries/*.conf"}
	// grubDefaults are what GRUB generates its configuration from: the edit that update-grub has to be run after.
	grubDefaults = []string{"/etc/default/grub", "/etc/default/grub.d/*.cfg"}
	// isWireless says whether an interface is a Wi-Fi one.
	isWireless = func(dev string) bool {
		_, err := os.Stat("/sys/class/net/" + dev + "/wireless")
		return err == nil
	}
)

// safeInterfaceName is an interface name that can go into a kernel argument, and a line of shell, unquoted.
var safeInterfaceName = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,15}$`)

var bootNetworkCheckName = T("the initrd gets an address the way this machine does")

type ipRoute struct {
	Gateway  string `json:"gateway"`
	Dev      string `json:"dev"`
	Protocol string `json:"protocol"`
	Prefsrc  string `json:"prefsrc"`
}

type ipAddr struct {
	AddrInfo []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
		Scope     string `json:"scope"`
		Dynamic   bool   `json:"dynamic"`
	} `json:"addr_info"`
}

type ipLink struct {
	LinkInfo struct {
		Kind string `json:"info_kind"`
	} `json:"linkinfo"`
}

// ipJSON asks ip(8) for JSON and reads the list it prints into out.
func ipJSON(out any, args ...string) error {
	raw, err := ipOutput(append([]string{"-j"}, args...)...)
	if err != nil {
		return fmt.Errorf(T("could not run ip: %w"), err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf(T("could not read what ip printed: %v"), err)
	}
	return nil
}

// bootNetwork is how the running system gets its address, as far as the initrd is concerned.
type bootNetwork struct {
	Dev  string
	DHCP bool   // the initrd's own DHCP gives the same: nothing to add
	Addr string // otherwise the address it has, and
	Args string // the kernel arguments that give the initrd it
}

// detectBootNetwork says how the interface with the default route gets its address, or the one
// named in only when it is not empty. An error is a reason it could not tell, which is not a
// problem of the machine's.
func detectBootNetwork(only string) (bootNetwork, error) {
	var routes []ipRoute
	if err := ipJSON(&routes, "-4", "route", "show", "default"); err != nil {
		return bootNetwork{}, err
	}
	if only != "" {
		kept := routes[:0]
		for _, r := range routes {
			if r.Dev == only {
				kept = append(kept, r)
			}
		}
		if routes = kept; len(routes) == 0 {
			return bootNetwork{}, fmt.Errorf(T("%s has no default route over IPv4, so there is no address to compare"), only)
		}
	}
	if len(routes) == 0 {
		return bootNetwork{}, errors.New(T("this machine has no default route over IPv4, so there is no address to compare"))
	}
	for _, r := range routes {
		if r.Dev != routes[0].Dev {
			return bootNetwork{}, errors.New(T("more than one interface has a default route; name one with --interface"))
		}
	}
	route := routes[0]
	dev := route.Dev
	// The line is meant to be pasted into /etc/default/grub, which update-grub sources in a root shell: an
	// interface name may hold anything the kernel allows (all but "/", ":" and white space), so only names that
	// are safe there are taken.
	if !safeInterfaceName.MatchString(dev) {
		return bootNetwork{}, fmt.Errorf(T("the interface %q has a name that is not safe to put in a kernel argument, so there is none to suggest"), dev)
	}

	if isWireless(dev) {
		return bootNetwork{}, fmt.Errorf(T("%s is a wireless interface, and the initrd cannot join a wireless network"), dev)
	}
	var links []ipLink
	if err := ipJSON(&links, "-d", "link", "show", "dev", dev); err != nil {
		return bootNetwork{}, err
	}
	if len(links) > 0 && links[0].LinkInfo.Kind != "" {
		return bootNetwork{}, fmt.Errorf(T("%s is a %s, which the initrd does not set up by itself"), dev, links[0].LinkInfo.Kind)
	}

	if route.Protocol == "dhcp" {
		return bootNetwork{Dev: dev, DHCP: true}, nil
	}

	if route.Gateway == "" {
		return bootNetwork{}, errors.New(T("the default route has no gateway, so there is no ip= argument to suggest"))
	}
	var addrs []ipAddr
	if err := ipJSON(&addrs, "-4", "addr", "show", "dev", dev); err != nil {
		return bootNetwork{}, err
	}
	local, prefix, leased := "", 0, false
	for _, a := range addrs {
		for _, i := range a.AddrInfo {
			if i.Family != "inet" || i.Scope != "global" {
				continue
			}
			if local == "" || i.Local == route.Prefsrc {
				local, prefix, leased = i.Local, i.PrefixLen, i.Dynamic
			}
		}
	}
	if local == "" {
		return bootNetwork{}, fmt.Errorf(T("%s has no global IPv4 address to suggest"), dev)
	}
	// ifupdown with dhclient and udhcpc scripts add the default route with a plain "ip route add", which the
	// kernel records as "boot", not "dhcp". The address says it was leased, and a lease is DHCP: pinning it
	// would hand the address to the initrd for good, after the lease has gone to somebody else.
	if leased {
		return bootNetwork{Dev: dev, DHCP: true}, nil
	}
	// Whatever ip printed, only addresses go into the line.
	for _, v := range []string{local, route.Gateway} {
		if addr, err := netip.ParseAddr(v); err != nil || !addr.Is4() || addr.IsUnspecified() {
			return bootNetwork{}, fmt.Errorf(T("%q is not an IPv4 address, so there is no ip= argument to suggest"), v)
		}
	}

	arg := fmt.Sprintf("ip=%s::%s:%s::%s:none", local, route.Gateway, net.IP(net.CIDRMask(prefix, 32)), dev)
	for _, dns := range upstreamDNS() {
		arg += " nameserver=" + dns
	}
	return bootNetwork{Dev: dev, Addr: local, Args: arg}, nil
}

// bootNetworkCheck says whether the initrd's DHCP would give it the network this machine has now. The
// command line is this boot's. It does not judge what it cannot tell and says why.
func bootNetworkCheck(cmdline string) rehearsalCheck {
	for _, f := range strings.Fields(cmdline) {
		if strings.HasPrefix(f, "ip=") {
			return rehearsalCheck{bootNetworkCheckName, "unchecked", T("the kernel command line has an ip= argument, which was not compared with this machine's network")}
		}
	}
	n, err := detectBootNetwork("")
	switch {
	case err != nil:
		return rehearsalCheck{bootNetworkCheckName, "unchecked", err.Error()}
	case n.DHCP:
		return rehearsalCheck{bootNetworkCheckName, "ok", ""}
	}
	return staticNetworkCheck(n)
}

// bootEntry is the kernel command line of one entry of the boot loader.
type bootEntry struct {
	File string
	Args []string
}

// readBootEntries reads the entries the boot loader would start a kernel from, which are the ones with
// no "recovery" or "single" in them: those are for repair, and a machine that waits for a key holder does
// not boot into them by itself.
func readBootEntries() []bootEntry {
	var entries []bootEntry
	for _, glob := range bootLoaderConfigs {
		files, _ := filepath.Glob(glob)
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(raw), "\n") {
				fields := strings.Fields(line)
				var args []string
				switch {
				case len(fields) == 0:
					continue
				case filepath.Ext(file) == ".conf" && fields[0] == "options":
					args = fields[1:]
				case filepath.Ext(file) != ".conf" && (fields[0] == "linux" || fields[0] == "linuxefi" || fields[0] == "linux16") && len(fields) > 2:
					args = fields[2:] // after the kernel image
				default:
					continue
				}
				if slices.Contains(args, "recovery") || slices.Contains(args, "single") {
					continue
				}
				entries = append(entries, bootEntry{file, args})
			}
		}
	}
	return entries
}

// ipArgs are the ip= arguments among args, as the text after "ip=". Only a whole argument is one: rd.ip= is
// another.
func ipArgs(args []string) []string {
	var found []string
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "ip="); ok {
			found = append(found, v)
		}
	}
	return found
}

// ipArgumentWord finds an ip= argument in a line of GRUB's defaults, where it sits inside quotes.
var ipArgumentWord = regexp.MustCompile(`(?:^|[\s"'=])ip=[^\s"']`)

// grubDefaultsHaveIP says whether the settings update-grub generates its configuration from carry an ip=
// argument.
func grubDefaultsHaveIP() (string, bool) {
	for _, glob := range grubDefaults {
		files, _ := filepath.Glob(glob)
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(raw), "\n") {
				if line = strings.TrimSpace(line); strings.HasPrefix(line, "GRUB_CMDLINE_LINUX") && ipArgumentWord.MatchString(line) {
					return file, true
				}
			}
		}
	}
	return "", false
}

// staticNetworkCheck is the check for a machine whose address is set by hand: the running boot has no ip=
// argument (one that has is dealt with before), so what matters is the next boot, and that is in the boot
// loader's configuration. A person who edited the defaults and ran update-grub has done everything.
func staticNetworkCheck(n bootNetwork) rehearsalCheck {
	add := fmt.Sprintf(T("add this to the kernel command line in the boot loader (for GRUB, GRUB_CMDLINE_LINUX in /etc/default/grub, then update-grub; synctang-unlocker network-args prints it again): %s"), n.Args)
	problem := func(detail string) rehearsalCheck { return rehearsalCheck{bootNetworkCheckName, "problem", detail} }

	entries := readBootEntries()
	if len(entries) == 0 {
		// Nothing of the boot loader could be read, so all there is to say is what to add.
		return rehearsalCheck{bootNetworkCheckName, "unverified", fmt.Sprintf(T("%s has an address that was set by hand, not by DHCP, and the initrd asks for DHCP only, so it has no network at boot unless the kernel command line gives it one; the boot loader's configuration could not be read, so it cannot be told whether it does; %s"), n.Dev, add)}
	}
	have, other := 0, ""
	for _, e := range entries {
		matched := false
		for _, v := range ipArgs(e.Args) {
			if client, _, _ := strings.Cut(v, ":"); client == n.Addr {
				matched = true
			} else if other == "" {
				other = client
			}
		}
		if matched {
			have++
		}
	}
	switch {
	case have == len(entries):
		return rehearsalCheck{bootNetworkCheckName, "ok", fmt.Sprintf(
			T("the boot loader's configuration (%s) has it for the next boot; this boot started without it, so reboot to use it"), entries[0].File)}
	case have == 0 && other != "":
		return problem(fmt.Sprintf(T("%s has an address that was set by hand, and the boot loader's configuration (%s) has an ip= argument for %s, which is not this machine's address %s; %s"), n.Dev, entries[0].File, other, n.Addr, add))
	case have == 0:
		if file, ok := grubDefaultsHaveIP(); ok {
			return problem(fmt.Sprintf(T("%s has an address that was set by hand, and %s has an ip= argument but the boot loader's configuration (%s) does not: run update-grub as root, then rehearse again"), n.Dev, file, entries[0].File))
		}
		return problem(fmt.Sprintf(T("%s has an address that was set by hand, not by DHCP, and the initrd asks for DHCP only, so it would have no network at boot; %s"), n.Dev, add))
	}
	return problem(fmt.Sprintf(T("%s has an address that was set by hand, and only %d of %d boot entries in the boot loader's configuration carry it, so the others would boot with no network; %s"), n.Dev, have, len(entries), add))
}

// upstreamDNS is up to two IPv4 DNS servers of the network, from the first file that lists any that
// is not a local stub.
func upstreamDNS() []string {
	for _, path := range upstreamResolvConfs {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		var servers []string
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 || fields[0] != "nameserver" {
				continue
			}
			if addr, err := netip.ParseAddr(fields[1]); err == nil && addr.Is4() && !addr.IsLoopback() {
				servers = append(servers, fields[1])
			}
		}
		f.Close()
		if len(servers) > 0 {
			return servers[:min(len(servers), 2)]
		}
	}
	return nil
}
