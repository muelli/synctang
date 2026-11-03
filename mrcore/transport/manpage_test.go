// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"strings"
	"testing"

	"github.com/muelli/synctang/internal/manpagecheck"
)

// The keys of the file are exactly the tags of the KEYS section of its page,
// and the options a discovery URL takes are described.
func TestNetworkConfManPageDocumentsEveryKey(t *testing.T) {
	page := manpagecheck.Page(t, "../../man/synctang-network.conf.5")
	keys, ok := manpagecheck.Section(page, "KEYS")
	if !ok {
		t.Fatal("the page has no KEYS section")
	}
	have := manpagecheck.Tags(keys)
	want := []string{NetworkKeyRelayPool, NetworkKeyDiscovery, NetworkKeyDiscoveryAnnounce}
	if m := manpagecheck.Missing(want, have); len(m) > 0 {
		t.Errorf("keys the file has and the page does not describe: %v", m)
	}
	if m := manpagecheck.Missing(have, want); len(m) > 0 {
		t.Errorf("the page describes keys the file does not have: %v", m)
	}
	for _, s := range []string{"?id=", "?insecure", "relay://"} {
		if !strings.Contains(page, s) {
			t.Errorf("the page does not describe %q", s)
		}
	}
	for _, name := range []string{"NAME", "DESCRIPTION", "FORMAT", "KEYS", "DISCOVERY URLS", "PRECEDENCE", "FILES", "SEE ALSO"} {
		if _, ok := manpagecheck.Section(page, name); !ok {
			t.Errorf("the page has no %s section", name)
		}
	}
}
