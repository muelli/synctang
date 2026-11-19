// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
)

// The machine says in its greeting what it will do with the answer when that is not an unlock. The app reads it
// from the session, to warn the person and to stop the one-tap path, and it is shown only cleaned: it is the
// machine's own word.

func connectedTo(t *testing.T, hello mrcore.Hello) *Session {
	t.Helper()
	machineSeed, phoneSeed := "purpose machine "+hello.Purpose, "purpose phone"
	phoneID, _ := DeviceIDForSeed(phoneSeed)
	machineID, _ := DeviceIDForSeed(machineSeed)
	machineCert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatal(err)
	}
	addr := freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	go func() {
		conn, err := transport.NewSyncthingRelay(machineCert).Listen(ctx, transport.ListenOptions{AuthorizedPeers: []string{phoneID}, DirectAddr: addr})
		if err != nil {
			return
		}
		defer conn.Close()
		_ = mrcore.WriteMessage(conn, hello)
		_ = mrcore.WriteMessage(conn, mrcore.RecoverRequest{Kid: make([]byte, 32), X: validPoint(t)})
		time.Sleep(time.Second)
	}()
	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(10)
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return s
}

func validPoint(t *testing.T) []byte {
	t.Helper()
	g := mrcore.P256()
	k, err := g.RandomScalar()
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.ScalarBaseMult(k)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTheSessionOffersThePurposeTheMachineGave(t *testing.T) {
	for _, purpose := range []string{mrcore.PurposeEnrol, mrcore.PurposePair, mrcore.PurposeExtract} {
		if got := connectedTo(t, mrcore.Hello{Name: "m", Purpose: purpose}).Purpose(); got != purpose {
			t.Errorf("Purpose = %q, want %q", got, purpose)
		}
	}
}

func TestAnOrdinaryUnlockHasNoPurpose(t *testing.T) {
	if got := connectedTo(t, mrcore.Hello{Name: "m"}).Purpose(); got != "" {
		t.Errorf("Purpose = %q, want none", got)
	}
}

// A purpose the library does not know is still a purpose, cleaned of what would act on a display, and cut.
func TestAPurposeTheLibraryDoesNotKnowIsOfferedCleaned(t *testing.T) {
	got := connectedTo(t, mrcore.Hello{Name: "m", Purpose: "wipe\x1b[31m\nit"}).Purpose()
	if got == "" || got == "wipe\x1b[31m\nit" || len(got) > 80 {
		t.Errorf("Purpose = %q", got)
	}
}
