// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	socket "syncthing-socket"
)

// pairRetryInterval matches connectRetryInterval: the machine may not
// have reached its listening state by the time the code is scanned off
// its screen, and a direct dial fails immediately rather than waiting.
const pairRetryInterval = 2 * time.Second

// Pair asks a machine to enrol this key holder, proving possession of
// the one-time code from the machine's QR code.
//
// This is the half of enrolment that a QR code cannot carry on its own.
// The code goes machine to phone, which is the easy direction: the
// machine has a screen and the phone a camera. What has to travel the
// other way is this key holder's public key, and it comes over the
// ordinary transport instead of being copied by hand.
//
// What deliberately does not travel either way is the volume's
// passphrase. The comparable design in syncthing-socket puts the
// passphrase itself in the QR code, which is what lets a single scan
// finish the job there; MR-1 never puts it on the phone at all.
//
// Takes the public key as hex because that is what the Kotlin side
// already has: the key lives in the Android Keystore and only its
// public half ever leaves it.
func Pair(machineID, pairingCode, seed, publicKeyHex, name, directAddr string, timeoutSeconds int) error {
	secret, err := mrcore.DecodePairingSecret(pairingCode)
	if err != nil {
		return fmt.Errorf("mobile: %w", err)
	}
	defer mrcore.Wipe(secret)

	publicKey, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("mobile: the public key is not valid hex: %w", err)
	}

	cert, err := socket.GenerateDeterministicCert(seed)
	if err != nil {
		return fmt.Errorf("mobile: deriving this key holder's identity: %w", err)
	}
	selfID, err := DeviceIDForSeed(seed)
	if err != nil {
		return fmt.Errorf("mobile: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	var lastErr error
	for {
		lastErr = pairOnce(ctx, cert, machineID, selfID, secret, publicKey, name, directAddr)
		if lastErr == nil {
			return nil
		}
		// The machine answering "no" is final: retrying cannot make a
		// wrong code right or change a human's mind. Only being unable
		// to reach it is worth another attempt.
		var refused *pairRefused
		if errors.As(lastErr, &refused) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("mobile: gave up pairing with %s after %ds (last attempt: %w)",
				machineID, timeoutSeconds, lastErr)
		case <-time.After(pairRetryInterval):
		}
	}
}

func pairOnce(ctx context.Context, cert tls.Certificate, machineID, selfID string, secret, publicKey []byte, name, directAddr string) error {
	tr := newDialTransport(cert, directAddr)
	conn, err := tr.Dial(ctx, transport.DialOptions{PeerID: machineID, DirectAddr: directAddr})
	if err != nil {
		return fmt.Errorf("mobile: dialling %s: %w", machineID, err)
	}
	defer conn.Close()

	var challenge mrcore.PairChallenge
	if err := mrcore.ReadMessage(conn, &challenge); err != nil {
		return fmt.Errorf("mobile: reading the pairing challenge: %w", err)
	}

	proof := mrcore.PairProof(secret, machineID, selfID, challenge.Nonce)
	req := mrcore.PairRequest{PubKey: publicKey, Name: name, Proof: proof}
	if err := mrcore.WriteMessage(conn, req); err != nil {
		return fmt.Errorf("mobile: sending the pairing request: %w", err)
	}

	var result mrcore.PairResult
	if err := mrcore.ReadMessage(conn, &result); err != nil {
		return fmt.Errorf("mobile: reading the pairing result: %w", err)
	}
	if !result.OK {
		return &pairRefused{reason: result.Error}
	}
	return nil
}

// pairRefused separates "the machine said no" from "the machine could
// not be reached", because only the second is worth retrying and only
// the first has something to tell the person holding the phone.
type pairRefused struct{ reason string }

func (e *pairRefused) Error() string {
	if e.reason == "" {
		return "the machine refused the pairing"
	}
	return "the machine refused the pairing: " + e.reason
}
