// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore/transport"
)

func TestNewDialTransportAppliesNetworkConfig(t *testing.T) {
	cert, err := transport.LoadOrCreateCert(t.TempDir() + "/id.pem")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := transport.ParseNetworkConfig("", "https://disco.example.net/v2/", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, direct := range map[string]string{"relay and local discovery": "", "direct address": "127.0.0.1:1"} {
		t.Run(name, func(t *testing.T) {
			var r *transport.SyncthingRelay
			switch v := newDialTransport(cert, direct, cfg).(type) {
			case *transport.SyncthingRelay:
				r = v
			case *transport.Multi:
				for _, leg := range v.Transports {
					if x, ok := leg.(*transport.SyncthingRelay); ok {
						r = x
					}
				}
			}
			if r == nil {
				t.Fatal("no SyncthingRelay leg")
			}
			if r.DiscoveryURL != "https://disco.example.net/v2/" {
				t.Errorf("DiscoveryURL = %q", r.DiscoveryURL)
			}
		})
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadNetworkConfigFlagsWinOverFile(t *testing.T) {
	conf := writeConfig(t, "discovery=https://file.example.net/v2/\ndiscovery-announce=https://file-announce.example.net/v2/\n")
	c, err := loadNetworkConfig(conf, true, "https://flag.example.net/v2/", "")
	if err != nil {
		t.Fatalf("loadNetworkConfig: %v", err)
	}
	if c.Discovery != "https://flag.example.net/v2/" {
		t.Errorf("the flag must win: %+v", c)
	}
	if c.DiscoveryAnnounce != "" {
		t.Errorf("the replaced server's announce endpoint must go with it: %+v", c)
	}
}

func TestLoadNetworkConfigMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "config")
	// The default location being empty is the ordinary case.
	c, err := loadNetworkConfig(missing, false, "", "")
	if err != nil || !c.IsZero() {
		t.Errorf("a missing default file means the defaults: %+v, %v", c, err)
	}
	// A file the person named and that is not there is a mistake.
	if _, err := loadNetworkConfig(missing, true, "", ""); err == nil {
		t.Error("an explicit --config that does not exist must be an error")
	}
}

func TestLoadNetworkConfigRefusesBadValues(t *testing.T) {
	if _, err := loadNetworkConfig("", false, "ftp://x.example.net/", ""); err == nil || !strings.Contains(err.Error(), "discovery") {
		t.Errorf("a bad flag must be refused, naming it: %v", err)
	}
	bad := writeConfig(t, "discovery=gopher://x.example.net/\n")
	if _, err := loadNetworkConfig(bad, true, "", ""); err == nil || !strings.Contains(err.Error(), bad) {
		t.Errorf("a bad file must be refused, naming the file: %v", err)
	}
}

// Only the machine joins a relay; a key holder learns it from discovery. A
// relay-pool line in its config would do nothing, so it is refused and says why,
// rather than being taken for a setting that works.
func TestKeyholderConfigRefusesRelayPool(t *testing.T) {
	conf := writeConfig(t, "relay-pool=https://pool.example.net/endpoint\n")
	_, err := loadNetworkConfig(conf, true, "", "")
	if err == nil {
		t.Fatal("a relay-pool line must be refused")
	}
	for _, want := range []string{conf, "relay-pool", "machine"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// There is no --relay-pool flag any more.
func TestRunUnlockHasNoRelayPoolFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"unlock", "--relay-pool", "https://pool.example.net/endpoint", "MACHINE"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "relay-pool") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
}

// The settings are read before the key is, so a typo is reported at
// once rather than after a dial that was never going to be the one meant.
func TestRunUnlockRefusesBadNetworkSettings(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"unlock", "--discovery", "ftp://x.example.net/", "MACHINE"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(stderr.String(), "--discovery") {
		t.Errorf("stderr %q should name the flag", stderr.String())
	}
}
