// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"path/filepath"
	"testing"

	"github.com/syncthing/syncthing/lib/protocol"
)

// TestLoadOrCreateCertStableAcrossRestarts is the point of LoadOrCreateCert:
// the Device ID derived from the certificate it hands back must not change
// between two calls against the same path, or every stored
// Recipient.Transport / AuthorizedPeers match silently breaks on the next
// restart.
func TestLoadOrCreateCertStableAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.pem")

	certA, err := LoadOrCreateCert(path)
	if err != nil {
		t.Fatalf("first LoadOrCreateCert: %v", err)
	}
	certB, err := LoadOrCreateCert(path)
	if err != nil {
		t.Fatalf("second LoadOrCreateCert: %v", err)
	}

	idA := protocol.NewDeviceID(certA.Certificate[0])
	idB := protocol.NewDeviceID(certB.Certificate[0])
	if idA != idB {
		t.Fatalf("Device ID changed across restarts: first %s, second %s", idA, idB)
	}
}

// TestLoadOrCreateCertFreshPathsDiffer guards against a broken randomness
// source: two identities that have never shared a path must not collide.
func TestLoadOrCreateCertFreshPathsDiffer(t *testing.T) {
	dir := t.TempDir()

	certA, err := LoadOrCreateCert(filepath.Join(dir, "a.pem"))
	if err != nil {
		t.Fatalf("LoadOrCreateCert a: %v", err)
	}
	certB, err := LoadOrCreateCert(filepath.Join(dir, "b.pem"))
	if err != nil {
		t.Fatalf("LoadOrCreateCert b: %v", err)
	}

	idA := protocol.NewDeviceID(certA.Certificate[0])
	idB := protocol.NewDeviceID(certB.Certificate[0])
	if idA == idB {
		t.Fatalf("two fresh paths produced the same Device ID %s, randomness is broken", idA)
	}
}
