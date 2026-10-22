// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
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
//
// Pair cannot be interrupted; see Pairer for a pairing that can.
func Pair(machineID, pairingCode, seed, publicKeyHex, name, directAddr string, timeoutSeconds int) error {
	return NewPairer().Pair(machineID, pairingCode, seed, publicKeyHex, name, directAddr, timeoutSeconds)
}

// Pairer is one pairing attempt that another thread can cancel, so the
// app can offer a Cancel button instead of a spinner that runs out its
// whole timeout. gomobile cannot bind a context.Context, hence an
// object with a Cancel method rather than a context parameter.
//
// A Pairer is single-use: a Pair after Cancel fails without dialling.
type Pairer struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	cancelled bool
}

// NewPairer returns a Pairer ready for one Pair call.
func NewPairer() *Pairer { return &Pairer{} }

// Cancel interrupts a Pair in progress on another thread, which then
// returns an error promptly. It returns at once. If the machine had
// already accepted the request before the cancel landed, the phone
// may be enrolled even though Pair reports the cancellation; pairing
// again simply enrols it again.
func (p *Pairer) Cancel() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelled = true
	if p.cancel != nil {
		p.cancel()
	}
}

func (p *Pairer) arm(cancel context.CancelFunc) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cancel != nil && p.cancelled {
		return false
	}
	p.cancel = cancel
	return true
}

func (p *Pairer) isCancelled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cancelled
}

// Pair is the package-level Pair, interruptible by Cancel.
func (p *Pairer) Pair(machineID, pairingCode, seed, publicKeyHex, name, directAddr string, timeoutSeconds int) error {
	secret, err := mrcore.DecodePairingSecret(pairingCode)
	if err != nil {
		return fmt.Errorf("mobile: %w", err)
	}
	defer mrcore.Wipe(secret)

	publicKey, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("mobile: the public key is not valid hex: %w", err)
	}

	cert, err := identity.GenerateDeterministicCert(seed)
	if err != nil {
		return fmt.Errorf("mobile: deriving this key holder's identity: %w", err)
	}
	selfID, err := DeviceIDForSeed(seed)
	if err != nil {
		return fmt.Errorf("mobile: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	if !p.arm(cancel) {
		return errCancelled
	}
	defer p.arm(nil)

	var lastErr error
	for {
		lastErr = pairOnce(ctx, cert, machineID, selfID, secret, publicKey, name, directAddr)
		if lastErr == nil {
			return nil
		}
		if p.isCancelled() {
			return errCancelled
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
			if p.isCancelled() {
				return errCancelled
			}
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
	// As in Session.connectOnceLocked: the reads honour no context, so
	// a cancelled attempt closes the connection under them.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

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
