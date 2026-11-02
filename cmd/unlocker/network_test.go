// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore/transport"
)

// syncthingRelayLeg finds the SyncthingRelay inside what
// newAgentTransport returned, whichever shape it came in.
// A well-formed Device ID for the relay:// form.
const testRelayID = "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"

func syncthingRelayLeg(t *testing.T, tr transport.Transport) *transport.SyncthingRelay {
	t.Helper()
	switch v := tr.(type) {
	case *transport.SyncthingRelay:
		return v
	case *transport.Multi:
		for _, leg := range v.Transports {
			if r, ok := leg.(*transport.SyncthingRelay); ok {
				return r
			}
		}
	}
	t.Fatalf("no SyncthingRelay in %T", tr)
	return nil
}

func TestNewAgentTransportAppliesNetworkConfig(t *testing.T) {
	cert, err := transport.LoadOrCreateCert(t.TempDir() + "/id.pem")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := transport.ParseNetworkConfig("https://pool.example.net/endpoint", "https://disco.example.net/v2/", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, direct := range map[string]string{"relay and local discovery": "", "direct address": "127.0.0.1:0"} {
		t.Run(name, func(t *testing.T) {
			r := syncthingRelayLeg(t, newAgentTransport(cert, nil, direct, cfg))
			if r.RelayPoolURL != "dynamic+https://pool.example.net/endpoint" {
				t.Errorf("RelayPoolURL = %q", r.RelayPoolURL)
			}
			if r.DiscoveryURL != "https://disco.example.net/v2/" {
				t.Errorf("DiscoveryURL = %q", r.DiscoveryURL)
			}
			if len(r.AnnounceURLs) != 1 || r.AnnounceURLs[0] != "https://disco.example.net/v2/" {
				t.Errorf("AnnounceURLs = %v", r.AnnounceURLs)
			}
		})
	}
}

func TestNewAgentTransportZeroConfigKeepsDefaults(t *testing.T) {
	cert, err := transport.LoadOrCreateCert(t.TempDir() + "/id.pem")
	if err != nil {
		t.Fatal(err)
	}
	r := syncthingRelayLeg(t, newAgentTransport(cert, nil, "", transport.NetworkConfig{}))
	if r.RelayPoolURL != "" || r.DiscoveryURL != "" || len(r.AnnounceURLs) != 0 {
		t.Errorf("defaults must stay in place: %+v", r)
	}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadNetworkConfigNeitherPresentMeansDefaults(t *testing.T) {
	dir := t.TempDir()
	c, err := loadNetworkConfig(filepath.Join(dir, "absent.conf"), filepath.Join(dir, "absent-cmdline"))
	if err != nil {
		t.Fatalf("loadNetworkConfig: %v", err)
	}
	if !c.IsZero() {
		t.Errorf("got %+v, want the defaults", c)
	}
}

func TestLoadNetworkConfigFile(t *testing.T) {
	conf := writeFile(t, "relay-pool=https://pool.example.net/endpoint\ndiscovery=https://disco.example.net/v2/\n")
	c, err := loadNetworkConfig(conf, "")
	if err != nil {
		t.Fatalf("loadNetworkConfig: %v", err)
	}
	if c.RelayPool != "https://pool.example.net/endpoint" || c.Discovery != "https://disco.example.net/v2/" {
		t.Errorf("got %+v", c)
	}
}

func TestLoadNetworkConfigCmdlineWinsOverFile(t *testing.T) {
	conf := writeFile(t, "relay-pool=https://file.example.net/endpoint\ndiscovery=https://file.example.net/v2/\n")
	cmdline := writeFile(t, "BOOT_IMAGE=/vmlinuz root=/dev/mapper/x quiet rd.unlocker.discovery=https://cmd.example.net/v2/ rd.neednet=1\n")
	c, err := loadNetworkConfig(conf, cmdline)
	if err != nil {
		t.Fatalf("loadNetworkConfig: %v", err)
	}
	if c.Discovery != "https://cmd.example.net/v2/" {
		t.Errorf("the command line must win: %+v", c)
	}
	if c.RelayPool != "https://file.example.net/endpoint" {
		t.Errorf("a setting the command line leaves alone must come from the file: %+v", c)
	}
}

// A discovery server from the command line is a different server, so
// the file's announce endpoint, which belongs to the file's server,
// must not survive it: announcing to one server and looking up on
// another finds nothing.
func TestLoadNetworkConfigCmdlineDiscoveryDropsFileAnnounce(t *testing.T) {
	conf := writeFile(t, "discovery=https://file.example.net/v2/\ndiscovery-announce=https://file-announce.example.net/v2/\n")
	cmdline := writeFile(t, "rd.unlocker.discovery=https://cmd.example.net/v2/\n")
	c, err := loadNetworkConfig(conf, cmdline)
	if err != nil {
		t.Fatalf("loadNetworkConfig: %v", err)
	}
	if c.DiscoveryAnnounce != "" {
		t.Errorf("announce endpoint of the replaced server survived: %+v", c)
	}
}

func TestLoadNetworkConfigCmdlineRelay(t *testing.T) {
	cmdline := writeFile(t, "ro rd.unlocker.relay=relay://10.0.0.5:22067/?id="+testRelayID+" quiet\n")
	c, err := loadNetworkConfig("", cmdline)
	if err != nil {
		t.Fatalf("loadNetworkConfig: %v", err)
	}
	if c.RelayKind() != "single relay" {
		t.Errorf("got %+v", c)
	}
}

func TestLoadNetworkConfigIgnoresOtherCmdlineArguments(t *testing.T) {
	// rd.unlocker=0 and lookalikes are not settings of this kind.
	cmdline := writeFile(t, "rd.unlocker=0 rd.unlocker.relayx=junk xrd.unlocker.relay=junk\n")
	c, err := loadNetworkConfig("", cmdline)
	if err != nil {
		t.Fatalf("loadNetworkConfig: %v", err)
	}
	if !c.IsZero() {
		t.Errorf("got %+v", c)
	}
}

// A setting that is wrong must stop the agent and say so. Falling back
// to the public servers would announce, on the public servers, a
// machine whose owner had chosen not to.
func TestLoadNetworkConfigRefusesBadSettings(t *testing.T) {
	badConf := writeFile(t, "discovery=ftp://x.example.net/\n")
	if _, err := loadNetworkConfig(badConf, ""); err == nil || !strings.Contains(err.Error(), badConf) {
		t.Errorf("a bad file must be refused, naming the file; got %v", err)
	}
	badCmdline := writeFile(t, "rd.unlocker.relay=gopher://x.example.net/\n")
	if _, err := loadNetworkConfig("", badCmdline); err == nil || !strings.Contains(err.Error(), "rd.unlocker.relay") {
		t.Errorf("a bad command line setting must be refused, naming it; got %v", err)
	}
	if _, err := loadNetworkConfig(t.TempDir(), ""); err == nil {
		t.Error("an unreadable file (a directory) must be an error, not silently the defaults")
	}
}

func TestResolveNetwork(t *testing.T) {
	oldCmdline := procCmdline
	defer func() { procCmdline = oldCmdline }()
	procCmdline = filepath.Join(t.TempDir(), "absent")

	var out, errOut strings.Builder
	good := writeFile(t, "discovery=https://disco.example.net/v2/\n")
	c, ok := resolveNetwork(good, &out, &errOut)
	if !ok || c.Discovery != "https://disco.example.net/v2/" {
		t.Fatalf("got %+v, %v, stderr %q", c, ok, errOut.String())
	}
	if !strings.Contains(out.String(), "disco.example.net") {
		t.Errorf("the person should be told which server is used; stdout %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	bad := writeFile(t, "discovery=ftp://x.example.net/\n")
	if _, ok := resolveNetwork(bad, &out, &errOut); ok {
		t.Error("a bad file must not be accepted")
	}
	if !strings.Contains(errOut.String(), "unlocker: ") || !strings.Contains(errOut.String(), bad) {
		t.Errorf("stderr %q should name the file", errOut.String())
	}

	out.Reset()
	if _, ok := resolveNetwork(filepath.Join(t.TempDir(), "none"), &out, &errOut); !ok || out.Len() != 0 {
		t.Errorf("no file, no remark: ok %v stdout %q", ok, out.String())
	}
}
