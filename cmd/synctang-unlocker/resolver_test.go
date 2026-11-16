// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
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
// synctang-unlocker's very first ensureResolver check accepted exactly this
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

// The agent calls ensureResolver on every pass, because at the time the hook starts the network is usually not up
// and nothing says where DNS is yet. When nothing is found it writes public resolvers (so that a lookup can at least
// be tried); but the next pass then found a resolv.conf with real nameservers in it and stopped looking, so the DNS
// that DHCP offered a moment later was never used: on a network where the public resolvers cannot be reached, the
// agent never found the relay or the discovery server.
func TestEnsureResolverTakesTheNetworksDNSWhenItAppearsAfterTheFallback(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/resolv.conf"
	leases := dir + "/leases"
	if err := os.Mkdir(leases, 0o755); err != nil {
		t.Fatal(err)
	}
	pattern := leases + "/*"

	ensureResolverAt(path, nil, []string{pattern})
	first, _ := os.ReadFile(path)
	if string(first) != fallbackResolvConf {
		t.Fatalf("nothing offered DNS yet, so the fallback was expected: %q", first)
	}

	if err := os.WriteFile(leases+"/2", []byte("DNS=10.0.0.1 10.0.0.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureResolverAt(path, nil, []string{pattern})
	second, _ := os.ReadFile(path)
	if want := "nameserver 10.0.0.1\nnameserver 10.0.0.2\n"; string(second) != want {
		t.Errorf("the DNS from DHCP was not taken after the fallback: %q, want %q", second, want)
	}
}

func TestEnsureResolverLeavesAResolvConfThatIsNotItsOwnFallbackAlone(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/resolv.conf"
	if err := os.WriteFile(path, []byte("nameserver 192.168.1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := dir + "/other"
	if err := os.WriteFile(other, []byte("nameserver 10.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureResolverAt(path, []string{other}, nil)
	if got, _ := os.ReadFile(path); string(got) != "nameserver 192.168.1.1\n" {
		t.Errorf("a usable resolv.conf was replaced: %q", got)
	}
}

// Said once, not at every pass (a pass is every two seconds): it is a note about the boot, not a progress line.
func TestEnsureResolverSaysItFellBackOnceAndNotOnEveryPass(t *testing.T) {
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	var warnings atomic.Int32
	slog.SetDefault(slog.New(countingWarnHandler{&warnings}))
	resolverFellBack = false

	path := t.TempDir() + "/resolv.conf"
	for i := 0; i < 6; i++ {
		ensureResolverAt(path, nil, nil)
	}
	if got := warnings.Load(); got != 1 {
		t.Errorf("%d warnings in 6 passes, want 1", got)
	}
}

type countingWarnHandler struct{ n *atomic.Int32 }

func (countingWarnHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h countingWarnHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn {
		h.n.Add(1)
	}
	return nil
}
func (h countingWarnHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h countingWarnHandler) WithGroup(string) slog.Handler      { return h }
