// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"strings"
	"testing"
)

// A real, well-formed Device ID, for the relay:// form, which carries
// the relay's own identity in its query.
const testRelayID = "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"

func TestParseNetworkConfigEmptyMeansDefaults(t *testing.T) {
	c, err := ParseNetworkConfig("", "", "")
	if err != nil {
		t.Fatalf("ParseNetworkConfig: %v", err)
	}
	if !c.IsZero() {
		t.Errorf("empty settings should be zero, got %+v", c)
	}
	tr := c.NewSyncthingRelay(testCert(t))
	if tr.RelayPoolURL != "" || tr.DiscoveryURL != "" || len(tr.AnnounceURLs) != 0 {
		t.Errorf("zero config must leave every default alone, got pool %q discovery %q announce %v",
			tr.RelayPoolURL, tr.DiscoveryURL, tr.AnnounceURLs)
	}
}

func TestParseNetworkConfigPoolForms(t *testing.T) {
	tests := []struct {
		in       string
		wantPool string
		wantKind string
	}{
		{"https://pool.example.net/endpoint", "dynamic+https://pool.example.net/endpoint", "pool"},
		{"http://10.0.0.5:22128/endpoint", "dynamic+http://10.0.0.5:22128/endpoint", "pool"},
		{"dynamic+https://pool.example.net/endpoint", "dynamic+https://pool.example.net/endpoint", "pool"},
		{"relay://10.0.0.5:22067/?id=" + testRelayID, "relay://10.0.0.5:22067/?id=" + testRelayID, "single relay"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			c, err := ParseNetworkConfig(tt.in, "", "")
			if err != nil {
				t.Fatalf("ParseNetworkConfig(%q): %v", tt.in, err)
			}
			if got := c.RelayPoolURL(); got != tt.wantPool {
				t.Errorf("RelayPoolURL() = %q, want %q", got, tt.wantPool)
			}
			if got := c.RelayKind(); got != tt.wantKind {
				t.Errorf("RelayKind() = %q, want %q", got, tt.wantKind)
			}
			if c.RelayPool != tt.in {
				t.Errorf("RelayPool = %q, want the text as given, %q", c.RelayPool, tt.in)
			}
			if tr := c.NewSyncthingRelay(testCert(t)); tr.RelayPoolURL != tt.wantPool {
				t.Errorf("SyncthingRelay.RelayPoolURL = %q, want %q", tr.RelayPoolURL, tt.wantPool)
			}
		})
	}
}

func TestParseNetworkConfigRejects(t *testing.T) {
	tests := []struct {
		name                      string
		pool, discovery, announce string
		wantErr                   string
	}{
		{"pool ftp", "ftp://pool.example.net/", "", "", "relay-pool"},
		{"pool bare host", "pool.example.net", "", "", "relay-pool"},
		{"pool no host", "https:///endpoint", "", "", "relay-pool"},
		{"relay without id", "relay://10.0.0.5:22067/", "", "", "id"},
		{"relay with bad id", "relay://10.0.0.5:22067/?id=nonsense", "", "", "id"},
		{"relay without port", "relay://10.0.0.5/?id=" + testRelayID, "", "", "port"},
		{"discovery ftp", "", "ftp://disco.example.net/v2/", "", "discovery"},
		{"discovery relay scheme", "", "relay://disco.example.net:1/", "", "discovery"},
		{"discovery bare host", "", "disco.example.net", "", "discovery"},
		{"discovery with credentials", "", "https://user:pw@disco.example.net/v2/", "", "credentials"},
		{"discovery with a space", "", "https://disco.example.net/a b", "", "discovery"},
		{"discovery with a newline", "", "https://disco.example.net/\nrelay-pool=x", "", "discovery"},
		{"announce ftp", "", "", "ftp://disco.example.net/v2/", "discovery-announce"},
		{"pool with credentials", "https://user:pw@pool.example.net/", "", "", "credentials"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseNetworkConfig(tt.pool, tt.discovery, tt.announce)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should mention %q", err, tt.wantErr)
			}
		})
	}
}

// An error must not echo a URL's userinfo back: it ends up in a journal
// and on a console.
func TestParseNetworkConfigErrorDoesNotEchoCredentials(t *testing.T) {
	_, err := ParseNetworkConfig("", "https://user:hunter2@disco.example.net/v2/", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error leaks the password: %v", err)
	}
}

func TestNetworkConfigDiscoveryDerivesAnnounce(t *testing.T) {
	c, err := ParseNetworkConfig("", "https://disco.example.net/v2/", "")
	if err != nil {
		t.Fatalf("ParseNetworkConfig: %v", err)
	}
	tr := c.NewSyncthingRelay(testCert(t))
	if tr.DiscoveryURL != "https://disco.example.net/v2/" {
		t.Errorf("DiscoveryURL = %q", tr.DiscoveryURL)
	}
	if len(tr.AnnounceURLs) != 1 || tr.AnnounceURLs[0] != "https://disco.example.net/v2/" {
		t.Errorf("AnnounceURLs = %v, want the discovery URL itself", tr.AnnounceURLs)
	}
}

func TestNetworkConfigExplicitAnnounceWins(t *testing.T) {
	c, err := ParseNetworkConfig("", "https://disco.example.net/v2/", "https://announce.example.net/v2/")
	if err != nil {
		t.Fatalf("ParseNetworkConfig: %v", err)
	}
	tr := c.NewSyncthingRelay(testCert(t))
	if tr.DiscoveryURL != "https://disco.example.net/v2/" {
		t.Errorf("DiscoveryURL = %q", tr.DiscoveryURL)
	}
	if len(tr.AnnounceURLs) != 1 || tr.AnnounceURLs[0] != "https://announce.example.net/v2/" {
		t.Errorf("AnnounceURLs = %v, want only the explicit announce URL", tr.AnnounceURLs)
	}
}

// Announcing to the public servers while looking up on a private one
// would publish this machine's relay address to the very servers the
// person chose not to use, so a private discovery URL must never leave
// the public announce URLs in place.
func TestNetworkConfigPrivateDiscoveryNeverAnnouncesPublicly(t *testing.T) {
	c, err := ParseNetworkConfig("https://pool.example.net/endpoint", "https://10.0.0.6:8443/v2/", "")
	if err != nil {
		t.Fatalf("ParseNetworkConfig: %v", err)
	}
	tr := c.NewSyncthingRelay(testCert(t))
	for _, u := range tr.announceURLs() {
		if strings.Contains(u, "syncthing.net") {
			t.Errorf("announce URL %q is public", u)
		}
	}
}

func TestParseNetworkConf(t *testing.T) {
	in := `# the owner's relay
relay-pool = https://pool.example.net/endpoint

discovery=https://disco.example.net/v2/
  discovery-announce = https://announce.example.net/v2/  
`
	c, err := ParseNetworkConf(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseNetworkConf: %v", err)
	}
	if c.RelayPool != "https://pool.example.net/endpoint" ||
		c.Discovery != "https://disco.example.net/v2/" ||
		c.DiscoveryAnnounce != "https://announce.example.net/v2/" {
		t.Errorf("got %+v", c)
	}
}

func TestParseNetworkConfRejects(t *testing.T) {
	tests := []struct {
		name, in, wantErr string
	}{
		{"unknown key", "relay_pool=https://x.example.net/\n", "line 1"},
		{"no equals", "relay-pool\n", "line 1"},
		{"duplicate", "discovery=https://a.example.net/\ndiscovery=https://b.example.net/\n", "line 2"},
		{"bad value", "\ndiscovery=ftp://a.example.net/\n", "discovery"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseNetworkConf(strings.NewReader(tt.in))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseNetworkConfEmptyValueIsUnset(t *testing.T) {
	c, err := ParseNetworkConf(strings.NewReader("relay-pool=\n"))
	if err != nil {
		t.Fatalf("ParseNetworkConf: %v", err)
	}
	if !c.IsZero() {
		t.Errorf("got %+v, want zero", c)
	}
}

func TestNetworkConfigOverride(t *testing.T) {
	base, _ := ParseNetworkConfig("https://file.example.net/endpoint", "https://file.example.net/v2/", "")
	over, _ := ParseNetworkConfig("", "https://cmd.example.net/v2/", "")
	got := base.Override(over)
	if got.RelayPool != "https://file.example.net/endpoint" {
		t.Errorf("an unset override must keep the base: %+v", got)
	}
	if got.Discovery != "https://cmd.example.net/v2/" {
		t.Errorf("a set override must win: %+v", got)
	}
}

// A discovery server that replaces another is a different server, so
// the announce endpoint that belonged to the old one must go with it:
// announcing to one and looking up on another finds nothing.
func TestNetworkConfigOverrideDiscoveryReplacesItsAnnounce(t *testing.T) {
	base, _ := ParseNetworkConfig("", "https://file.example.net/v2/", "https://file-announce.example.net/v2/")
	over, _ := ParseNetworkConfig("", "https://cmd.example.net/v2/", "")
	got := base.Override(over)
	if got.DiscoveryAnnounce != "" {
		t.Errorf("announce endpoint of the replaced server survived: %+v", got)
	}
	// An override that names its own announce endpoint keeps it.
	over, _ = ParseNetworkConfig("", "https://cmd.example.net/v2/", "https://cmd-announce.example.net/v2/")
	if got := base.Override(over); got.DiscoveryAnnounce != "https://cmd-announce.example.net/v2/" {
		t.Errorf("got %+v", got)
	}
	// Overriding something else leaves the announce endpoint alone.
	over, _ = ParseNetworkConfig("https://pool.example.net/endpoint", "", "")
	if got := base.Override(over); got.DiscoveryAnnounce != "https://file-announce.example.net/v2/" {
		t.Errorf("got %+v", got)
	}
}

func TestNetworkConfigSummary(t *testing.T) {
	if got := (NetworkConfig{}).Summary(); got != "" {
		t.Errorf("defaults need no remark, got %q", got)
	}
	c, _ := ParseNetworkConfig("relay://10.0.0.5:22067/?id="+testRelayID, "https://disco.example.net/v2/", "")
	got := c.Summary()
	for _, want := range []string{"single relay", "10.0.0.5:22067", "disco.example.net"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q should mention %q", got, want)
		}
	}
	c, _ = ParseNetworkConfig("https://pool.example.net/endpoint", "", "")
	if got := c.Summary(); !strings.Contains(got, "pool") || !strings.Contains(got, "public") {
		t.Errorf("summary %q should say a pool is used and that discovery stays public", got)
	}
}

// Plain http is for looking up and never for announcing, so the setting a
// machine announces to has to be https, and says why when it is not.
func TestMachineDiscoveryRefusesPlainHTTP(t *testing.T) {
	_, err := ParseNetworkConfig("", "http://10.0.0.6:8443/v2/?insecure", "")
	if err == nil {
		t.Fatal("a machine's discovery server over plain http was accepted; it would announce over it")
	}
	for _, want := range []string{"discovery", "http", "announce"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
	if _, err := ParseNetworkConfig("", "https://10.0.0.6:8443/v2/", "http://10.0.0.6:8443/v2/?insecure"); err == nil {
		t.Error("an announce endpoint over plain http was accepted")
	}
	// With its own https announce endpoint, the lookup address may be plain
	// http (it goes to the phone and is only looked up on).
	if _, err := ParseNetworkConfig("", "http://10.0.0.6:8443/v2/?insecure", "https://10.0.0.6:8444/v2/"); err != nil {
		t.Errorf("lookup-only http discovery with an https announce endpoint: %v", err)
	}
}

func TestDialerDiscoveryMayBePlainHTTPWithInsecure(t *testing.T) {
	c, err := ParseDialerNetworkConfig("http://10.0.0.6:8443/v2/?insecure", "")
	if err != nil {
		t.Fatalf("ParseDialerNetworkConfig: %v", err)
	}
	if c.Discovery != "http://10.0.0.6:8443/v2/?insecure" {
		t.Errorf("got %+v", c)
	}
	for _, bad := range []string{
		"http://10.0.0.6:8443/v2/",                   // plain http with no explicit opt-in
		"http://10.0.0.6:8443/v2/?insecure=false",    // opt-in refused
		"http://10.0.0.6:8443/v2/?id=" + testRelayID, // an id pins a TLS certificate; there is none
		"https://10.0.0.6:8443/v2/?id=not-an-id",     // not a Device ID
	} {
		if _, err := ParseDialerNetworkConfig(bad, ""); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := ParseDialerNetworkConfig("https://10.0.0.6:8443/v2/?id="+testRelayID, ""); err != nil {
		t.Errorf("a pinned https server: %v", err)
	}
	if _, err := ParseDialerNetworkConfig("", "http://10.0.0.6:8443/v2/?insecure"); err == nil {
		t.Error("a dialling side's announce endpoint over plain http was accepted")
	}
}

func TestNetworkConfigDiscoveryIDIsValidated(t *testing.T) {
	if _, err := ParseNetworkConfig("", "https://disco.example.net/v2/?id="+testRelayID, ""); err != nil {
		t.Errorf("a pinned discovery server: %v", err)
	}
	_, err := ParseNetworkConfig("", "https://disco.example.net/v2/?id=nonsense", "")
	if err == nil || !strings.Contains(err.Error(), "Device ID") {
		t.Errorf("a malformed id must be refused saying it is not a Device ID: %v", err)
	}
}
