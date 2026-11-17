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
	"strings"
)

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
		return exec.Command("ip", args...).Output() // i18n:skip a command, not a message
	}
	// upstreamResolvConfs are the files that list the DNS servers of the network itself, best first: the
	// stub resolver in /etc/resolv.conf answers on a local address that the initrd does not have.
	upstreamResolvConfs = []string{"/run/systemd/resolve/resolv.conf", "/run/NetworkManager/resolv.conf", "/etc/resolv.conf"}
	// isWireless says whether an interface is a Wi-Fi one.
	isWireless = func(dev string) bool {
		_, err := os.Stat("/sys/class/net/" + dev + "/wireless")
		return err == nil
	}
)

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
	Args string // otherwise the kernel arguments that give the initrd this machine's address
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
	local, prefix := "", 0
	for _, a := range addrs {
		for _, i := range a.AddrInfo {
			if i.Family != "inet" || i.Scope != "global" {
				continue
			}
			if local == "" || i.Local == route.Prefsrc {
				local, prefix = i.Local, i.PrefixLen
			}
		}
	}
	if local == "" {
		return bootNetwork{}, fmt.Errorf(T("%s has no global IPv4 address to suggest"), dev)
	}

	arg := fmt.Sprintf("ip=%s::%s:%s::%s:none", local, route.Gateway, net.IP(net.CIDRMask(prefix, 32)), dev)
	for _, dns := range upstreamDNS() {
		arg += " nameserver=" + dns
	}
	return bootNetwork{Dev: dev, Args: arg}, nil
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
	return rehearsalCheck{bootNetworkCheckName, "problem", fmt.Sprintf(
		T("%s has an address that was set by hand, not by DHCP, and the initrd asks for DHCP only, so it would have no network at boot; add this to the kernel command line in the boot loader (for GRUB, GRUB_CMDLINE_LINUX in /etc/default/grub, then update-grub; synctang-unlocker network-args prints it again): %s"), n.Dev, n.Args)}
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
