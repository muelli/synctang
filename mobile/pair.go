// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
)

// defaultPairOperatorWait is how long the phone waits, once the machine has
// the proof of the pairing code, for the person at the machine to answer "enrol
// it on this volume? [y/N]".
//
// The phone cannot know how long the machine will wait: "synctang-unlocker
// pair" keeps its window for --timeout, five minutes by default, and goes on to
// enrol after the phone has stopped waiting, which made a phone report failure
// on a success when the operator took about a minute. So the wait is its own
// budget, separate from the one for reaching the machine, and matched to that
// default; Cancel ends it at any time.
const defaultPairOperatorWait = 5 * time.Minute

// pairOperatorWait is defaultPairOperatorWait; a variable for the tests.
var pairOperatorWait = defaultPairOperatorWait

// errOperatorTimeout is what a wait for the operator that ran out reports.
var errOperatorTimeout = withKind(FailurePairNotConfirmed,
	errors.New("mobile: the machine did not confirm the pairing in time"))

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

	failure lastFailure

	// progress is read by Stage, Attempt and Route; see Session.
	progress progress

	// network is the machine's choice of discovery server,
	// from its pairing link; the zero value is the public ones.
	network transport.NetworkConfig
}

// LastFailure is the kind of failure the last Pair ended in (one of
// the Failure constants), or FailureNone if it succeeded.
func (p *Pairer) LastFailure() int {
	kind, _ := p.failure.get()
	return kind
}

// LastFailureDetail is the machine's own reason for refusing, when it
// gave one the app has no words of its own for. Empty otherwise.
func (p *Pairer) LastFailureDetail() string {
	_, detail := p.failure.get()
	return detail
}

// SetDiscovery is Session.SetDiscovery for a pairing: look the machine up
// on the discovery server its pairing link named. Call it before Pair.
func (p *Pairer) SetDiscovery(discovery string) error {
	network, err := transport.ParseDialerNetworkConfig(discovery, "")
	if err != nil {
		return fmt.Errorf("mobile: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.network = network
	return nil
}

// NewPairer returns a Pairer ready for one Pair call.
func NewPairer() *Pairer {
	p := &Pairer{}
	p.progress.label = "pairing"
	return p
}

// Cancel interrupts a Pair in progress on another thread, which then
// returns an error promptly. It returns at once. If the machine had
// already accepted the request before the cancel landed, the phone
// may be enrolled even though Pair reports the cancellation; pairing
// again simply enrols it again.
func (p *Pairer) Cancel() {
	logEvent(slog.LevelDebug, "cancel", "who", "pairing", "stage", stageName(p.Stage()))
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
func (p *Pairer) Pair(machineID, pairingCode, seed, publicKeyHex, name, directAddr string, timeoutSeconds int) (err error) {
	defer func() {
		p.failure.record(err)
		if err != nil {
			kind, _ := p.failure.get()
			logEvent(slog.LevelWarn, "pairing failed", "machine", machineID, "kind", kind, "attempts", p.Attempt(), "error", err)
		}
	}()
	secret, err := mrcore.DecodePairingSecret(pairingCode)
	if err != nil {
		return withKind(FailureBadPairingCode, fmt.Errorf("mobile: %w", err))
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

	// ctx ends only on Cancel. dialCtx bounds reaching the machine and sending the
	// proof, by timeoutSeconds; the wait for the operator after that has a budget of
	// its own (pairOperatorWait), so a slow decision is not mistaken for a machine
	// that could not be reached.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dialCtx, cancelDial := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancelDial()
	if !p.arm(cancel) {
		return errCancelled
	}
	defer p.arm(nil)
	p.progress.advance(StageStarting)

	p.mu.Lock()
	network := p.network
	p.mu.Unlock()
	// Never the code, the proof, the public key or the seed: only that there is a code.
	logEvent(slog.LevelDebug, "pairing starting", "machine", machineID, "has_code", pairingCode != "", "direct_addr", directAddr,
		"discovery", network.Discovery, "dial_budget_s", timeoutSeconds)

	var lastErr error
	for {
		lastErr = pairOnce(ctx, dialCtx, &p.progress, cert, machineID, selfID, secret, publicKey, name, directAddr, network)
		if lastErr == nil {
			logEvent(slog.LevelDebug, "pairing succeeded", "machine", machineID, "attempts", p.Attempt(), "route", routeName(p.Route()))
			return nil
		}
		logEvent(slog.LevelDebug, "attempt failed", "who", "pairing", "attempt", p.Attempt(), "error", lastErr)
		if p.isCancelled() {
			return errCancelled
		}
		// The machine answering "no" is final: retrying cannot make a
		// wrong code right or change a human's mind. Only being unable
		// to reach it is worth another attempt.
		var refused *pairRefused
		if errors.As(lastErr, &refused) {
			return refused.withKind()
		}
		// The machine was reached and has the proof; a fresh dial would only
		// spend the one-time code again.
		if errors.Is(lastErr, errOperatorTimeout) {
			return lastErr
		}
		select {
		case <-dialCtx.Done():
			if p.isCancelled() {
				return errCancelled
			}
			return withKind(FailureUnreachable, fmt.Errorf("mobile: gave up pairing with %s after %ds (last attempt: %w)",
				machineID, timeoutSeconds, lastErr))
		case <-time.After(pairRetryInterval):
		}
	}
}

func pairOnce(ctx, dialCtx context.Context, prog *progress, cert tls.Certificate, machineID, selfID string, secret, publicKey []byte, name, directAddr string, network transport.NetworkConfig) error {
	prog.beginAttempt(firstStage(directAddr))
	tr := newProgressDialTransport(cert, directAddr, network, prog)
	conn, err := tr.Dial(dialCtx, transport.DialOptions{PeerID: machineID, DirectAddr: directAddr})
	if err != nil {
		return fmt.Errorf("mobile: dialling %s: %w", machineID, err)
	}
	if directAddr != "" {
		prog.won(0)
	}
	defer conn.Close()
	// As in Session.connectOnceLocked: the reads honour no context, so
	// a cancelled attempt closes the connection under them. Until the proof is
	// sent that is the dial budget; the wait for the operator has its own below.
	stop := context.AfterFunc(dialCtx, func() { conn.Close() })
	defer func() { stop() }()

	var challenge mrcore.PairChallenge
	if err := mrcore.ReadMessage(conn, &challenge); err != nil {
		return fmt.Errorf("mobile: reading the pairing challenge: %w", err)
	}

	proof := mrcore.PairProof(secret, machineID, selfID, publicKey, challenge.Nonce)
	req := mrcore.PairRequest{PubKey: publicKey, Name: name, Proof: proof}
	prog.advance(StageSendingProof)
	if err := mrcore.WriteMessage(conn, req); err != nil {
		return fmt.Errorf("mobile: sending the pairing request: %w", err)
	}
	stop()
	waitCtx, cancelWait := context.WithTimeout(ctx, pairOperatorWait)
	defer cancelWait()
	stop = context.AfterFunc(waitCtx, func() { conn.Close() })
	prog.advance(StageWaitingForOperator)

	var result mrcore.PairResult
	if err := mrcore.ReadMessage(conn, &result); err != nil {
		if waitCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return errOperatorTimeout
		}
		return fmt.Errorf("mobile: reading the pairing result: %w", err)
	}
	if !result.OK {
		return &pairRefused{reason: result.Error}
	}
	prog.advance(StageEnrolled)
	return nil
}

// pairRefused separates "the machine said no" from "the machine could
// not be reached", because only the second is worth retrying and only
// the first has something to tell the person holding the phone.
type pairRefused struct{ reason string }

// withKind names the refusal. The reasons the app has words of its own
// for get a kind and no detail; any other is passed on as the detail,
// so a newer machine's reason still reaches the person.
func (e *pairRefused) withKind() error {
	switch e.reason {
	case mrcore.PairRefusedCodeMismatch:
		return withKind(FailurePairCodeMismatch, e)
	case mrcore.PairRefusedDeclined:
		return withKind(FailurePairDeclined, e)
	default:
		return withKindDetail(FailurePairRefused, e.reason, e)
	}
}

func (e *pairRefused) Error() string {
	if e.reason == "" {
		return "the machine refused the pairing"
	}
	return "the machine refused the pairing: " + e.reason
}
