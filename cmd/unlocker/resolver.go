// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const resolvConfPath = "/etc/resolv.conf"

// resolverSources are the files dracut's various network modules leave
// a resolver in, in preference order. Which one appears depends on
// which network module happened to be pulled into the initrd
// (systemd-networkd, NetworkManager, or the legacy dhclient path), so
// all of them are worth trying.
var resolverSources = []string{
	"/run/systemd/resolve/resolv.conf",
	"/run/NetworkManager/resolv.conf",
	"/tmp/net.*.resolv.conf",
}

// networkdStateFiles are where systemd-networkd records what DHCP told
// it, tried when none of resolverSources produced anything: on a
// dracut initrd without systemd-resolved (this project's target,
// Ubuntu 26.04's default), this is the only place a nameserver
// appears at all.
var networkdStateFiles = []string{
	"/run/systemd/netif/state",
	"/run/systemd/netif/leases/*",
}

// looksLikeResolvConf reports whether content has at least one
// nameserver line, so an empty or comment-only file is not mistaken
// for a working resolver.
func looksLikeResolvConf(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "nameserver ") {
			return true
		}
	}
	return false
}

// dnsFromNetworkdState pulls nameservers out of a systemd-networkd
// state or lease file's "DNS=" line.
func dnsFromNetworkdState(content string) []string {
	var servers []string
	for _, line := range strings.Split(content, "\n") {
		value, found := strings.CutPrefix(strings.TrimSpace(line), "DNS=")
		if !found {
			continue
		}
		servers = append(servers, strings.Fields(value)...)
	}
	return servers
}

// resolvConfFromNetworkdState renders a resolv.conf from a
// systemd-networkd state or lease file's content directly, without
// the caller needing dnsFromNetworkdState's intermediate slice.
func resolvConfFromNetworkdState(content string) string {
	var b strings.Builder
	for _, server := range dnsFromNetworkdState(content) {
		fmt.Fprintf(&b, "nameserver %s\n", server)
	}
	return b.String()
}

// ensureResolver makes sure /etc/resolv.conf exists and has at least
// one nameserver before any DNS-dependent network call is attempted.
//
// This hook is started from dracut's initqueue "settled", which fires
// once udev has settled, not once the network is up: at that exact
// moment there is usually no resolver yet, so this cannot be checked
// once at agent startup, only on every retry (the caller is
// responsible for calling this repeatedly, not just once).
func ensureResolver() {
	if info, err := os.Stat(resolvConfPath); err == nil && info.Size() > 0 {
		if content, err := os.ReadFile(resolvConfPath); err == nil && looksLikeResolvConf(string(content)) {
			return
		}
	}

	for _, pattern := range resolverSources {
		for _, candidate := range globAll(pattern) {
			content, err := os.ReadFile(candidate)
			if err != nil || !looksLikeResolvConf(string(content)) {
				continue
			}
			if os.WriteFile(resolvConfPath, content, 0o644) == nil {
				return
			}
		}
	}

	for _, pattern := range networkdStateFiles {
		for _, candidate := range globAll(pattern) {
			content, err := os.ReadFile(candidate)
			if err != nil {
				continue
			}
			rendered := resolvConfFromNetworkdState(string(content))
			if rendered == "" {
				continue
			}
			if os.WriteFile(resolvConfPath, []byte(rendered), 0o644) == nil {
				return
			}
		}
	}
}

// globAll expands pattern, treating a plain path with no wildcard as a
// one-element match so callers can mix the two without caring which is
// which.
func globAll(pattern string) []string {
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{pattern}
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}
