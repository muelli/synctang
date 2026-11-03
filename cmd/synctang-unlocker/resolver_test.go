// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"testing"
)

func TestParseResolvConfSourceValid(t *testing.T) {
	got := looksLikeResolvConf("nameserver 192.168.1.1\n")
	if !got {
		t.Fatal("expected a nameserver line to look like a usable resolv.conf")
	}
}

func TestParseResolvConfSourceEmpty(t *testing.T) {
	if looksLikeResolvConf("") {
		t.Fatal("empty content must not look like a usable resolv.conf")
	}
	if looksLikeResolvConf("# just a comment\n") {
		t.Fatal("a comment-only file must not look like a usable resolv.conf")
	}
}

// A resolv.conf whose only nameservers are loopback addresses is what
// systemd-resolved leaves behind (127.0.0.53, ::1): it looks present
// and non-empty, but is only useful once systemd-resolved itself is
// actually running with an upstream configured, which this early in a
// dracut initrd it is not. Found running this against a real VM:
// unlocker's very first ensureResolver check accepted exactly this
// and never looked any further, so it retried DNS against a resolver
// that was actively refusing connections, forever.
func TestLooksLikeResolvConfRejectsLoopbackOnly(t *testing.T) {
	cases := []string{
		"nameserver 127.0.0.53\n",
		"nameserver ::1\n",
		"nameserver 127.0.0.1\noptions edns0\n",
	}
	for _, c := range cases {
		if looksLikeResolvConf(c) {
			t.Fatalf("looksLikeResolvConf(%q) = true, want false (loopback-only)", c)
		}
	}
}

func TestLooksLikeResolvConfAcceptsMixedLoopbackAndReal(t *testing.T) {
	if !looksLikeResolvConf("nameserver 127.0.0.53\nnameserver 192.168.1.1\n") {
		t.Fatal("expected a resolv.conf with at least one real nameserver to look usable")
	}
}

func TestDNSFromNetworkdState(t *testing.T) {
	const state = `# This is private data. Do not parse.
DNS=192.168.1.1 192.168.1.2
NTP=192.168.1.1
`
	got := dnsFromNetworkdState(state)
	want := []string{"192.168.1.1", "192.168.1.2"}
	if len(got) != len(want) {
		t.Fatalf("dnsFromNetworkdState: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dnsFromNetworkdState: got %v want %v", got, want)
		}
	}
}

func TestDNSFromNetworkdStateNoDNSLine(t *testing.T) {
	got := dnsFromNetworkdState("NTP=192.168.1.1\n")
	if len(got) != 0 {
		t.Fatalf("expected no servers without a DNS= line, got %v", got)
	}
}

func TestResolvConfFromNetworkdState(t *testing.T) {
	const state = "DNS=9.9.9.9\n"
	got := resolvConfFromNetworkdState(state)
	want := "nameserver 9.9.9.9\n"
	if got != want {
		t.Fatalf("resolvConfFromNetworkdState: got %q want %q", got, want)
	}
}

func TestEnsureResolverFallsBackToPublicResolvers(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/resolv.conf"

	// No resolverSources/networkdStateFiles exist in this sandboxed
	// test, so the only remaining path is the last-resort fallback.
	ensureResolverAt(path, nil, nil)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !looksLikeResolvConf(string(content)) {
		t.Fatalf("fallback resolv.conf does not look usable: %q", content)
	}
}
