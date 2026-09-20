// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"bytes"
	"context"
	"crypto/elliptic"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	socket "syncthing-socket"
)

// vectors mirrors the parts of testdata/mr1.json this package needs.
// The Android instrumented test reads the very same file and drives
// the very same bound methods, so a divergence between the two shows
// up as a test failure on one side or the other rather than as a
// protocol bug found at unlock time.
type vectors struct {
	Recipient struct {
		SScalar string `json:"s_scalar"`
		S       string `json:"S"`
	} `json:"recipient"`
	Enrol struct {
		C     string `json:"C"`
		Kid   string `json:"kid"`
		P     string `json:"P"`
		Nonce string `json:"nonce"`
		Ct    string `json:"ct"`
	} `json:"enrol"`
	Recover struct {
		EScalar string `json:"e_scalar"`
		X       string `json:"X"`
		XOnly   string `json:"xOnly"`
	} `json:"recover"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("../testdata/mr1.json")
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decoding vectors: %v", err)
	}
	return v
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decoding hex %q: %v", s, err)
	}
	return b
}

func recipientFromVectors(t *testing.T, v vectors) *Recipient {
	t.Helper()
	r := NewRecipient()
	r.Kid = mustHex(t, v.Enrol.Kid)
	r.S = mustHex(t, v.Recipient.S)
	r.C = mustHex(t, v.Enrol.C)
	r.Ciphertext = mustHex(t, v.Enrol.Ct)
	r.Nonce = mustHex(t, v.Enrol.Nonce)
	return r
}

// TestRecipientFinishXOnlyMatchesVector is the proof the gomobile
// binding is worth building an app on: the exact call the Android
// instrumented test makes, made here first from Go.
func TestRecipientFinishXOnlyMatchesVector(t *testing.T) {
	v := loadVectors(t)
	got, err := recipientFromVectors(t, v).FinishXOnly(
		mustHex(t, v.Recover.EScalar),
		mustHex(t, v.Recover.XOnly),
	)
	if err != nil {
		t.Fatalf("FinishXOnly: %v", err)
	}
	if want := mustHex(t, v.Enrol.P); !bytes.Equal(got, want) {
		t.Fatalf("recovered secret\n got %x\nwant %x", got, want)
	}
}

// TestRecipientFinishXOnlyRejectsWrongAnswer checks a wrong
// x-coordinate is an error rather than a panic or a wrong secret: the
// AEAD tag is what decides, and both lifted sign candidates must fail.
func TestRecipientFinishXOnlyRejectsWrongAnswer(t *testing.T) {
	v := loadVectors(t)
	wrong := mustHex(t, v.Recover.XOnly)
	wrong[0] ^= 0xff
	if _, err := recipientFromVectors(t, v).FinishXOnly(mustHex(t, v.Recover.EScalar), wrong); err == nil {
		t.Fatal("expected an error from a wrong x-coordinate, got none")
	}
}

func TestAffineCoordinatesMatchVector(t *testing.T) {
	v := loadVectors(t)
	point := mustHex(t, v.Recover.X)

	x, err := AffineX(point)
	if err != nil {
		t.Fatalf("AffineX: %v", err)
	}
	y, err := AffineY(point)
	if err != nil {
		t.Fatalf("AffineY: %v", err)
	}
	if !bytes.Equal(point[1:33], x) || !bytes.Equal(point[33:], y) {
		t.Fatalf("affine split does not match the SEC1 encoding")
	}
	if _, err := AffineX([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected an error for a non-point, got none")
	}
}

// TestSessionRecoveryRoundTrip drives a whole key holder session
// against an in-process machine over a direct TCP address, with the
// key holder's ECDH done in software here in place of the Keystore.
// It covers everything the Android app asks of this package apart
// from the Keystore call itself, which no emulator-free test can
// exercise.
func TestSessionRecoveryRoundTrip(t *testing.T) {
	v := loadVectors(t)
	runSession(t, v, false)
}

// TestSessionConfirmCodeRoundTrip is the same flow with the machine
// running with a confirm code, which the key holder must relay back
// before it is shown any recovery request at all.
func TestSessionConfirmCodeRoundTrip(t *testing.T) {
	v := loadVectors(t)
	runSession(t, v, true)
}

func runSession(t *testing.T, v vectors, withConfirmCode bool) {
	t.Helper()

	const (
		addr        = "127.0.0.1:34517"
		phoneSeed   = "test phone identity seed"
		machineSeed = "test machine identity seed"
		machineName = "workstation in the study"
		confirmCode = "471 902"
	)

	phoneID, err := DeviceIDForSeed(phoneSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}

	machineCert, err := socket.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatalf("machine cert: %v", err)
	}

	recovered := make(chan []byte, 1)
	machineErr := make(chan error, 1)

	go func() {
		tr := transport.NewSyncthingRelay(machineCert)
		conn, err := tr.Listen(context.Background(), transport.ListenOptions{
			AuthorizedPeers: []string{phoneID},
			DirectAddr:      addr,
		})
		if err != nil {
			machineErr <- err
			return
		}
		defer conn.Close()

		if err := mrcore.WriteMessage(conn, mrcore.Hello{Name: machineName}); err != nil {
			machineErr <- err
			return
		}
		// Unconditional, exactly as cmd/unlocker/agent.go does it: the
		// machine always sends a ConfirmCodeChallenge in this position
		// and an empty Code means "not required". This used to be sent
		// only when withConfirmCode was set, which made the fake
		// machine the one thing in the project that did not follow the
		// convention, and hid a bug that made the app unable to unlock
		// any machine not running in confirm-code mode.
		code := ""
		if withConfirmCode {
			code = confirmCode
		}
		if err := mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: code}); err != nil {
			machineErr <- err
			return
		}
		if withConfirmCode {
			var answer mrcore.ConfirmCodeResponse
			if err := mrcore.ReadMessage(conn, &answer); err != nil {
				machineErr <- err
				return
			}
			if answer.Code != confirmCode {
				machineErr <- errWrongCode
				return
			}
		}

		req := mrcore.RecoverRequest{
			Kid: mustHex(t, v.Enrol.Kid),
			X:   mustHex(t, v.Recover.X),
		}
		if err := mrcore.WriteMessage(conn, req); err != nil {
			machineErr <- err
			return
		}

		var resp mrcore.RecoverResponse
		if err := mrcore.ReadMessage(conn, &resp); err != nil {
			machineErr <- err
			return
		}
		if resp.Error != "" {
			machineErr <- errKeyHolderDeclined
			return
		}

		p, err := recipientFromVectors(t, v).FinishXOnly(mustHex(t, v.Recover.EScalar), resp.XOnly)
		if err != nil {
			machineErr <- err
			return
		}
		// The real machine's last message, which the key holder now
		// waits for before claiming anything happened.
		if err := mrcore.WriteMessage(conn, mrcore.RecoverResult{OK: true}); err != nil {
			machineErr <- err
			return
		}
		recovered <- p
	}()

	session := NewSession(machineID, phoneSeed)
	session.SetDirectAddr(addr)
	session.SetTimeoutSeconds(20)
	defer session.Close()

	if err := session.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got := session.MachineName(); got != machineName {
		t.Fatalf("MachineName = %q, want %q", got, machineName)
	}
	if got := session.PeerID(); got != machineID {
		t.Fatalf("PeerID = %q, want %q", got, machineID)
	}

	if withConfirmCode {
		if got := session.ConfirmCodeChallenge(); got != confirmCode {
			t.Fatalf("ConfirmCodeChallenge = %q, want %q", got, confirmCode)
		}
		if session.HasRequest() {
			t.Fatal("a recovery request arrived before the confirm code was answered")
		}
		if err := session.SubmitConfirmCode(confirmCode); err != nil {
			t.Fatalf("SubmitConfirmCode: %v", err)
		}
	} else if got := session.ConfirmCodeChallenge(); got != "" {
		t.Fatalf("unexpected confirm code challenge %q", got)
	}

	if !session.HasRequest() {
		t.Fatal("no recovery request after connecting")
	}
	if !bytes.Equal(session.RequestKid(), mustHex(t, v.Enrol.Kid)) {
		t.Fatalf("RequestKid does not match the enrolled kid")
	}

	// Stand in for the Keystore: multiply the challenge point by the
	// key holder's long-term scalar and keep only the x-coordinate,
	// which is exactly what KeyAgreement.generateSecret() returns.
	px, err := session.RequestPointAffineX()
	if err != nil {
		t.Fatalf("RequestPointAffineX: %v", err)
	}
	py, err := session.RequestPointAffineY()
	if err != nil {
		t.Fatalf("RequestPointAffineY: %v", err)
	}
	xOnly := softwareECDH(t, mustHex(t, v.Recipient.SScalar), px, py)
	if want := mustHex(t, v.Recover.XOnly); !bytes.Equal(xOnly, want) {
		t.Fatalf("software ECDH\n got %x\nwant %x", xOnly, want)
	}

	if err := session.AnswerXOnly(xOnly); err != nil {
		t.Fatalf("AnswerXOnly: %v", err)
	}
	if err := session.AnswerXOnly(xOnly); err == nil {
		t.Fatal("expected the second answer to be refused")
	}

	select {
	case err := <-machineErr:
		t.Fatalf("machine side: %v", err)
	case p := <-recovered:
		if want := mustHex(t, v.Enrol.P); !bytes.Equal(p, want) {
			t.Fatalf("machine recovered\n got %x\nwant %x", p, want)
		}
	}
}

// softwareECDH is the stand-in for the Android Keystore in tests:
// x(scalar . (px, py)), returned as the raw 32-byte coordinate.
// elliptic.ScalarMult is deprecated but is the only standard library
// route to a bare scalar multiplication, and this is test-only code.
//
//nolint:staticcheck
func softwareECDH(t *testing.T, scalar, px, py []byte) []byte {
	t.Helper()
	x, _ := elliptic.P256().ScalarMult(new(big.Int).SetBytes(px), new(big.Int).SetBytes(py), scalar)
	out := make([]byte, coordLen)
	x.FillBytes(out)
	return out
}

// TestEnrolAndChallengeRoundTrip covers the loop the Android
// instrumented test runs against a real Keystore key, with a software
// scalar standing in for the hardware one here.
func TestEnrolAndChallengeRoundTrip(t *testing.T) {
	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	pub, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	ch, err := EnrolAndChallenge(pub)
	if err != nil {
		t.Fatalf("EnrolAndChallenge: %v", err)
	}

	px, err := AffineX(ch.Point())
	if err != nil {
		t.Fatalf("AffineX: %v", err)
	}
	py, err := AffineY(ch.Point())
	if err != nil {
		t.Fatalf("AffineY: %v", err)
	}

	recoveredSecret, err := ch.Finish(softwareECDH(t, s, px, py))
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if !bytes.Equal(recoveredSecret, ch.Secret()) {
		t.Fatalf("recovered\n got %x\nwant %x", recoveredSecret, ch.Secret())
	}
	if len(ch.Kid()) != 32 {
		t.Fatalf("kid should be a sha256 digest, got %d bytes", len(ch.Kid()))
	}
}

var (
	errWrongCode         = errString("confirm code did not match")
	errKeyHolderDeclined = errString("key holder declined")
)

type errString string

func (e errString) Error() string { return string(e) }

// A relay session can die between being established and being used,
// and both sides then see EOF partway through the exchange rather than
// a dial failure. The laptop client survives that by retrying the
// whole attempt (cmd/keyholder's runUnlock loop); a fresh attempt is a
// fresh, independent race on both sides, so retrying converges rather
// than repeating the same mismatch.
//
// The app did not retry. It made exactly one attempt and showed the
// raw error, so against a real machine over the real relay pool it
// usually failed while the laptop against the same machine did not.
// Seen on real hardware: "mobile: reading hello: EOF" on the phone at
// the same moment as "reading recover response: EOF" on the machine,
// with no unauthorized-peer rejection on either side.
func TestSessionConnectRetriesUntilTheMachineAnswers(t *testing.T) {
	v := loadVectors(t)

	const (
		addr        = "127.0.0.1:34519"
		phoneSeed   = "retry test phone seed"
		machineSeed = "retry test machine seed"
		machineName = "flaky machine"
		failures    = 2
	)

	phoneID, err := DeviceIDForSeed(phoneSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	machineCert, err := socket.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatalf("machine cert: %v", err)
	}

	served := make(chan int, 1)
	go func() {
		tr := transport.NewSyncthingRelay(machineCert)
		for attempt := 0; ; attempt++ {
			conn, err := tr.Listen(context.Background(), transport.ListenOptions{
				AuthorizedPeers: []string{phoneID},
				DirectAddr:      addr,
			})
			if err != nil {
				return
			}
			// The first few attempts die the way a dropped relay
			// session does: the peer is gone, nothing is written.
			if attempt < failures {
				conn.Close()
				continue
			}
			_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: machineName})
			_ = mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: ""})
			_ = mrcore.WriteMessage(conn, mrcore.RecoverRequest{
				Kid: mustHex(t, v.Enrol.Kid),
				X:   mustHex(t, v.Recover.X),
			})
			served <- attempt
			// Hold the connection open so the client can read it.
			time.Sleep(2 * time.Second)
			conn.Close()
			return
		}
	}()

	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(30)
	if err := s.Connect(); err != nil {
		t.Fatalf("Connect should have retried past the dropped sessions, got %v", err)
	}
	defer s.Close()

	if !s.HasRequest() {
		t.Fatal("expected a recovery request after a successful retry")
	}
	if s.MachineName() != machineName {
		t.Errorf("MachineName = %q, want %q", s.MachineName(), machineName)
	}
	select {
	case attempt := <-served:
		if attempt != failures {
			t.Errorf("machine served attempt %d, want %d", attempt, failures)
		}
	default:
		t.Fatal("machine never served a full exchange")
	}
}

// The machine's verdict is the whole point of the last message: a key
// holder must not report success when the machine could not use its
// answer. Reported by a human whose phone said the unlock had worked
// while the machine sat at its LUKS prompt.
func TestAnswerXOnlyReportsAMachineThatCouldNotUseTheAnswer(t *testing.T) {
	v := loadVectors(t)

	const (
		addr        = "127.0.0.1:34521"
		phoneSeed   = "verdict test phone seed"
		machineSeed = "verdict test machine seed"
	)

	phoneID, err := DeviceIDForSeed(phoneSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	machineCert, err := socket.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatalf("machine cert: %v", err)
	}

	go func() {
		tr := transport.NewSyncthingRelay(machineCert)
		conn, err := tr.Listen(context.Background(), transport.ListenOptions{
			AuthorizedPeers: []string{phoneID},
			DirectAddr:      addr,
		})
		if err != nil {
			return
		}
		defer conn.Close()
		_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: "grumpy machine"})
		_ = mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: ""})
		_ = mrcore.WriteMessage(conn, mrcore.RecoverRequest{
			Kid: mustHex(t, v.Enrol.Kid),
			X:   mustHex(t, v.Recover.X),
		})
		var resp mrcore.RecoverResponse
		_ = mrcore.ReadMessage(conn, &resp)
		_ = mrcore.WriteMessage(conn, mrcore.RecoverResult{
			OK:    false,
			Error: "recovered secret does not open /dev/vda3",
		})
		time.Sleep(time.Second)
	}()

	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(30)
	if err := s.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer s.Close()

	err = s.AnswerXOnly(make([]byte, 32))
	if err == nil {
		t.Fatal("AnswerXOnly reported success for an answer the machine could not use")
	}
	if !strings.Contains(err.Error(), "does not open /dev/vda3") {
		t.Errorf("the machine's own reason should reach the caller, got %v", err)
	}
}
