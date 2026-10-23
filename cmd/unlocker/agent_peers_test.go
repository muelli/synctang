// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

func recipientWithDevice(t *testing.T, id string) mrcore.Recipient {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"device_id": id})
	if err != nil {
		t.Fatal(err)
	}
	return mrcore.Recipient{Transport: raw}
}

// A token whose recipients carry no transport identity would give an
// empty authorised list, and an empty list means "accept anyone" to the
// transport. The agent must refuse to start rather than listen to
// strangers and fail to match them afterwards.
func TestAuthorizedPeersRefusesATokenWithNoTransportIdentity(t *testing.T) {
	tok := mrcore.Token{Recipients: []mrcore.Recipient{{}, {}}}
	_, err := authorizedPeersFor(tok)
	if err == nil {
		t.Fatal("authorizedPeersFor accepted a token with no recipient transport id")
	}
	if !strings.Contains(err.Error(), "recipient-transport-id") {
		t.Fatalf("error does not say how to fix it: %v", err)
	}
}

func TestAuthorizedPeersListsEveryRecipientThatHasOne(t *testing.T) {
	tok := mrcore.Token{Recipients: []mrcore.Recipient{
		recipientWithDevice(t, "AAAAAAA-BBBBBBB"),
		{},
		recipientWithDevice(t, "CCCCCCC-DDDDDDD"),
	}}
	got, err := authorizedPeersFor(tok)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "AAAAAAA-BBBBBBB" || got[1] != "CCCCCCC-DDDDDDD" {
		t.Fatalf("authorizedPeersFor = %v", got)
	}
}
