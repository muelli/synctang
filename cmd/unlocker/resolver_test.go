// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

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
