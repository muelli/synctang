// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"net"
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

// fallbackResolvers are queried directly (bypassing whatever, if
// anything, /etc/resolv.conf already says) only when nothing on disk
// offers a real nameserver at all: better to trust a public resolver
// for finding the relay pool's address than to sit retrying forever
// against a resolver that is not there yet. This is not a security
// weakening: a malicious DNS answer here could only misdirect which
// relay or discovery server the machine talks to, not anything about
// MR-1's cryptography, which is why cheating on DNS resolution is an
// acceptable trade for boot-time robustness.
var fallbackResolvConf = "nameserver 1.1.1.1\nnameserver 9.9.9.9\n"

// looksLikeResolvConf reports whether content has at least one
// nameserver line that is not a loopback address, so an empty,
// comment-only, or systemd-resolved-stub-only file (127.0.0.53, ::1,
// left behind whether or not systemd-resolved itself is actually
// running yet) is not mistaken for a working resolver. Found running
// this against a real VM: a loopback-only resolv.conf already exists
// by the time this hook runs, and the very first version of this
// check accepted it, so the agent retried DNS against a resolver that
// was actively refusing connections, forever.
func looksLikeResolvConf(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "nameserver ")
		if !ok {
			continue
		}
		if ip := net.ParseIP(strings.TrimSpace(rest)); ip != nil && ip.IsLoopback() {
			continue
		}
		return true
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
	ensureResolverAt(resolvConfPath, resolverSources, networkdStateFiles)
}

// ensureResolverAt is ensureResolver with every path parameterized,
// so the search can be exercised against fixtures instead of the
// real /etc/resolv.conf and dracut state files.
func ensureResolverAt(path string, sources, stateFiles []string) {
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		if content, err := os.ReadFile(path); err == nil && looksLikeResolvConf(string(content)) {
			return
		}
	}

	for _, pattern := range sources {
		for _, candidate := range globAll(pattern) {
			content, err := os.ReadFile(candidate)
			if err != nil || !looksLikeResolvConf(string(content)) {
				continue
			}
			if os.WriteFile(path, content, 0o644) == nil {
				return
			}
		}
	}

	for _, pattern := range stateFiles {
		for _, candidate := range globAll(pattern) {
			content, err := os.ReadFile(candidate)
			if err != nil {
				continue
			}
			rendered := resolvConfFromNetworkdState(string(content))
			if rendered == "" {
				continue
			}
			if os.WriteFile(path, []byte(rendered), 0o644) == nil {
				return
			}
		}
	}

	// Nothing on disk offered a real nameserver. Rather than retry
	// indefinitely against a resolver that is not there, or none at
	// all, fall back to a public one directly.
	os.WriteFile(path, []byte(fallbackResolvConf), 0o644)
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
