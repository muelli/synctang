// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mobile is the gomobile-bound surface of synctang's key holder
// side of MR-1. It exists so the Android app can reuse mrcore and
// mrcore/transport unchanged instead of reimplementing the protocol,
// the wire framing and the relay dialling in Kotlin.
//
// Everything exported here obeys gomobile's type restrictions: only
// strings, ints, bools, byte slices, errors, and pointers to structs
// declared in this package cross the language boundary. No Go
// interfaces, no slices of anything but bytes, no time.Time.
//
// The one thing deliberately NOT done here is the ECDH operation
// itself. The key holder's long-term scalar lives in the Android
// Keystore (ideally StrongBox) and is not readable by any process,
// including this one, so the multiplication has to happen in Kotlin
// via java.security.KeyAgreement. Session hands Kotlin the peer point
// and takes back the resulting x-coordinate; see AnswerXOnly.
package mobile

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	socket "syncthing-socket"
)

// uncompressedPointLen is the length of a SEC1 uncompressed P-256
// point: a 0x04 prefix followed by two 32-byte coordinates. mrcore
// encodes every point this way.
const uncompressedPointLen = 65

// coordLen is the length of one P-256 affine coordinate.
const coordLen = 32

// defaultTimeoutSeconds bounds a single connection attempt, including
// the relay lookup and the TLS handshake. A key holder that cannot
// reach the machine should say so rather than spin forever behind a
// progress indicator.
const defaultTimeoutSeconds = 60

// DeviceIDForSeed returns the Syncthing Device ID this key holder
// presents when it dials a machine, derived from seed. The machine
// authorises this string at pairing time, so the app shows it to the
// user and the user carries it (by QR code, or by reading it out) to
// the machine.
//
// The derivation is deterministic: the same seed always yields the
// same identity, so the app only has to persist the seed string and
// never a certificate blob.
func DeviceIDForSeed(seed string) (string, error) {
	cert, err := certForSeed(seed)
	if err != nil {
		return "", err
	}
	return protocol.NewDeviceID(cert.Certificate[0]).String(), nil
}

// certForSeed derives this key holder's transport certificate from
// seed. It is unexported because tls.Certificate is not a type
// gomobile can bind; the app only ever sees the Device ID string that
// falls out of it.
func certForSeed(seed string) (tls.Certificate, error) {
	if strings.TrimSpace(seed) == "" {
		return tls.Certificate{}, errors.New("mobile: empty identity seed")
	}
	cert, err := socket.GenerateDeterministicCert(seed)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("mobile: deriving identity: %w", err)
	}
	return cert, nil
}

// Recipient mirrors the fields of an enrolled MR-1 token entry that a
// recovery attempt needs. It is bound for one reason: so an Android
// instrumented test can drive the real Go recovery math over the fixed
// MR-1 vectors in testdata/mr1.json and check that the binding, and
// the arithmetic behind it, survive the trip through gomobile.
//
// The phone never performs this computation during a real unlock: the
// machine does, because only the machine holds the ephemeral scalar e.
type Recipient struct {
	Kid        []byte
	S          []byte
	C          []byte
	Ciphertext []byte
	Nonce      []byte
}

// NewRecipient returns an empty Recipient whose fields the caller
// fills in through the generated setters. gomobile cannot construct a
// struct with arguments, hence the empty constructor.
func NewRecipient() *Recipient { return &Recipient{} }

// FinishXOnly completes a recovery attempt from an x-only key holder's
// answer, exactly as mrcore.FinishXOnly does, and returns the
// recovered volume secret P. e is the ephemeral scalar from the
// matching challenge.
func (r *Recipient) FinishXOnly(e, xOnly []byte) ([]byte, error) {
	rec := mrcore.Recipient{
		Kid:        r.Kid,
		S:          r.S,
		C:          r.C,
		Ciphertext: r.Ciphertext,
		Nonce:      r.Nonce,
	}
	// mrcore.FinishXOnly wipes the scalar it is given. The caller's
	// slice is Java-owned here, so hand over a copy rather than
	// zeroing memory Kotlin still holds a reference to.
	return mrcore.FinishXOnly(mrcore.P256(), append([]byte(nil), e...), rec, xOnly)
}

// AffineX returns the 32-byte x-coordinate of an uncompressed SEC1
// point, and AffineY its y-coordinate. The Android side needs both to
// rebuild the peer point as a java.security.spec.ECPublicKeySpec, and
// parsing SEC1 is not something worth writing twice.
func AffineX(point []byte) ([]byte, error) { return affineCoord(point, 1) }

// AffineY returns the 32-byte y-coordinate of an uncompressed SEC1
// point. See AffineX.
func AffineY(point []byte) ([]byte, error) { return affineCoord(point, 1+coordLen) }

func affineCoord(point []byte, off int) ([]byte, error) {
	if len(point) != uncompressedPointLen || point[0] != 4 {
		return nil, fmt.Errorf("mobile: not an uncompressed P-256 point (%d bytes)", len(point))
	}
	return append([]byte(nil), point[off:off+coordLen]...), nil
}

// Session is one key holder side of an MR-1 recovery: dial the
// machine, learn who is asking, hand the challenge point to the
// Keystore, send the answer back.
//
// The phone always dials and the machine always listens, so a machine
// sitting at its unlock prompt never has to accept an inbound
// connection from the internet.
//
// A Session is single-use. Calling Connect a second time, or calling
// AnswerXOnly twice, is an error rather than a silent retry: each
// attempt is a distinct challenge that the user approved separately.
type Session struct {
	mu sync.Mutex

	seed      string
	machineID string

	// DirectAddr, when set before Connect, dials a plain "host:port"
	// and skips discovery and the relay pool entirely. It exists for
	// tests and for same-network pairing; the TLS identity handshake
	// runs either way.
	directAddr     string
	timeoutSeconds int

	conn        transport.Conn
	machineName string
	confirmCode string
	kid         []byte
	x           []byte
	answered    bool
}

// NewSession prepares a recovery attempt against machineDeviceID,
// identifying this phone by seed (see DeviceIDForSeed). Nothing
// happens on the network until Connect.
func NewSession(machineDeviceID, seed string) *Session {
	return &Session{
		machineID:      strings.TrimSpace(machineDeviceID),
		seed:           seed,
		timeoutSeconds: defaultTimeoutSeconds,
	}
}

// SetDirectAddr makes Connect dial addr ("host:port") directly instead
// of looking the machine up on the discovery server. Empty (the
// default) means use discovery and the relay network.
func (s *Session) SetDirectAddr(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.directAddr = addr
}

// SetTimeoutSeconds bounds Connect. Zero or negative restores the
// default.
func (s *Session) SetTimeoutSeconds(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		n = defaultTimeoutSeconds
	}
	s.timeoutSeconds = n
}

// Connect dials the machine, verifies its identity, and reads as far
// as the protocol allows without user input: the machine's Hello, and
// then either a confirm-code challenge (in which case the caller must
// show the code, collect the user's reading of it, and call
// SubmitConfirmCode) or the recovery request itself.
//
// After Connect returns without error, ConfirmCodeChallenge tells the
// caller which of the two happened.
func (s *Session) Connect() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn != nil {
		return errors.New("mobile: session already connected")
	}
	if s.machineID == "" {
		return errors.New("mobile: no machine device ID to dial")
	}

	cert, err := certForSeed(s.seed)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.timeoutSeconds)*time.Second)
	defer cancel()

	tr := transport.NewSyncthingRelay(cert)
	conn, err := tr.Dial(ctx, transport.DialOptions{
		PeerID:     s.machineID,
		DirectAddr: s.directAddr,
	})
	if err != nil {
		return fmt.Errorf("mobile: dialling %s: %w", s.machineID, err)
	}
	s.conn = conn

	var hello mrcore.Hello
	if err := mrcore.ReadMessage(conn, &hello); err != nil {
		s.closeLocked()
		return fmt.Errorf("mobile: reading hello: %w", err)
	}
	s.machineName = hello.Name

	return s.readChallengeLocked()
}

// readChallengeLocked reads the one message that follows Hello. The
// machine sends either a confirm-code challenge or the recovery
// request, and the MR-1 framing carries no message type tag, so the
// two are told apart by which fields the JSON actually names: a
// confirm-code challenge has a code and no point, a recovery request
// has a point and no code.
func (s *Session) readChallengeLocked() error {
	var msg struct {
		Code string `json:"code"`
		Kid  []byte `json:"kid"`
		X    []byte `json:"X"`
	}
	if err := mrcore.ReadMessage(s.conn, &msg); err != nil {
		s.closeLocked()
		return fmt.Errorf("mobile: reading challenge: %w", err)
	}

	switch {
	case len(msg.X) > 0:
		s.confirmCode = ""
		s.kid = msg.Kid
		s.x = msg.X
		return nil
	case msg.Code != "":
		s.confirmCode = msg.Code
		return nil
	default:
		s.closeLocked()
		return errors.New("mobile: machine sent neither a confirm code nor a recovery request")
	}
}

// MachineName is the name the machine gave for itself in its Hello. It
// is only a label: it is not authenticated by anything, so the app
// must show it as a hint alongside PeerID, never as proof of identity.
func (s *Session) MachineName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machineName
}

// PeerID is the machine's cryptographically verified Syncthing Device
// ID, derived from the certificate it proved possession of during the
// handshake.
func (s *Session) PeerID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return ""
	}
	return s.conn.PeerID()
}

// ConfirmCodeChallenge returns the code the machine is showing on its
// own console, or the empty string when the machine did not ask for
// one. A non-empty value means the caller must read the code off the
// machine, have the user confirm it matches, and call
// SubmitConfirmCode before any recovery request arrives.
func (s *Session) ConfirmCodeChallenge() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.confirmCode
}

// SubmitConfirmCode answers the machine's confirm-code challenge and
// then reads the recovery request that follows. The machine, not this
// code, decides whether the answer was right: a wrong code makes the
// machine hang up, which surfaces here as a read error.
func (s *Session) SubmitConfirmCode(code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return errors.New("mobile: not connected")
	}
	if s.confirmCode == "" {
		return errors.New("mobile: machine did not ask for a confirm code")
	}
	if err := mrcore.WriteMessage(s.conn, mrcore.ConfirmCodeResponse{Code: code}); err != nil {
		s.closeLocked()
		return fmt.Errorf("mobile: sending confirm code: %w", err)
	}
	s.confirmCode = ""
	return s.readChallengeLocked()
}

// RequestKid identifies which enrolled key holder the machine is
// asking for, so an app holding more than one key knows which one to
// use. It is sha256 of that key holder's long-term public point.
func (s *Session) RequestKid() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.kid...)
}

// RequestPoint is the challenge point X = C + E, SEC1 uncompressed.
// The key holder must return the x-coordinate of s.X, and nothing
// else: X is blinded by the machine's fresh ephemeral scalar, so
// answering it reveals nothing about any other challenge and the key
// holder learns nothing about the volume secret.
func (s *Session) RequestPoint() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.x...)
}

// RequestPointAffineX and RequestPointAffineY are RequestPoint split
// into the two raw coordinates the Android Keystore needs to rebuild
// the peer key.
func (s *Session) RequestPointAffineX() ([]byte, error) {
	return AffineX(s.RequestPoint())
}

// RequestPointAffineY returns the challenge point's y-coordinate. See
// RequestPointAffineX.
func (s *Session) RequestPointAffineY() ([]byte, error) {
	return AffineY(s.RequestPoint())
}

// HasRequest reports whether a recovery request has been received, so
// the caller can tell "still waiting on a confirm code" from "ready to
// answer" without comparing byte slices in Kotlin.
func (s *Session) HasRequest() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.x) > 0
}

// AnswerXOnly sends the x-coordinate of s.X back to the machine, which
// is the whole of a hardware-bound key holder's contribution: the
// machine lifts it back to a point and finishes the recovery itself.
// xOnly is what java.security.KeyAgreement.generateSecret() returns
// for an EC key, which is the raw coordinate and not a KDF output
// (confirmed on device, see docs/wp0-keystore-ecdh.md).
func (s *Session) AnswerXOnly(xOnly []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return errors.New("mobile: not connected")
	}
	if len(s.x) == 0 {
		return errors.New("mobile: no recovery request to answer")
	}
	if s.answered {
		return errors.New("mobile: this request has already been answered")
	}
	if len(xOnly) != coordLen {
		return fmt.Errorf("mobile: expected a %d-byte x-coordinate, got %d bytes", coordLen, len(xOnly))
	}

	if err := mrcore.WriteMessage(s.conn, mrcore.RecoverResponse{XOnly: xOnly}); err != nil {
		return fmt.Errorf("mobile: sending answer: %w", err)
	}
	s.answered = true
	return nil
}

// Decline tells the machine this key holder will not answer, so the
// machine can stop waiting and say why instead of timing out. Called
// when the user dismisses the biometric prompt or refuses the request.
func (s *Session) Decline(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return errors.New("mobile: not connected")
	}
	if reason == "" {
		reason = "declined by the key holder"
	}
	if err := mrcore.WriteMessage(s.conn, mrcore.RecoverResponse{Error: reason}); err != nil {
		return fmt.Errorf("mobile: sending decline: %w", err)
	}
	s.answered = true
	return nil
}

// Close releases the connection. It is safe to call more than once,
// and safe to call on a Session that never connected.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

func (s *Session) closeLocked() error {
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}
