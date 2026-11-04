// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

func TestParseLinkReadsWhatTheMachinePutsInIt(t *testing.T) {
	l, err := parseLink("synctang://pair?machine=77U3ZPL-3GZZIPN&name=e2e%20vm&psk=ORICASJ5&discovery=https%3A%2F%2F10.77.0.1%3A8443%2Fv2%2F%3Fid%3DABC")
	if err != nil {
		t.Fatal(err)
	}
	if l.machine != "77U3ZPL-3GZZIPN" || l.name != "e2e vm" || l.code != "ORICASJ5" {
		t.Fatalf("got %+v", l)
	}
	if l.discovery != "https://10.77.0.1:8443/v2/?id=ABC" {
		t.Fatalf("discovery = %q", l.discovery)
	}
}

func TestParseLinkWithoutADiscoveryServerLeavesItEmpty(t *testing.T) {
	l, err := parseLink("synctang://pair?machine=M&name=n&psk=P")
	if err != nil {
		t.Fatal(err)
	}
	if l.discovery != "" {
		t.Fatalf("discovery = %q", l.discovery)
	}
}

func TestParseLinkRefusesWhatIsNotAPairingLink(t *testing.T) {
	for _, bad := range []string{
		"",
		"https://pair?machine=M&psk=P",
		"synctang://other?machine=M&psk=P",
		"synctang://pair?psk=P",
		"synctang://pair?machine=M",
	} {
		if _, err := parseLink(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
