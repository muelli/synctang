// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
)

// cryptsetupAskIDPrefix restricts which /run/systemd/ask-password requests
// this agent will ever answer. systemd uses the same mechanism for SSH
// host keys and other unrelated prompts, and handing a recovered LUKS
// secret to whatever happens to be asking would be its own kind of bug.
const cryptsetupAskIDPrefix = "cryptsetup"

// runAgentOnce performs one recovery attempt against device: it waits for
// exactly one key holder to connect (via tr), runs the MR-1 recovery
// exchange (with an optional confirm-code check first), verifies the
// recovered secret actually opens device before trusting it, and answers
// the matching systemd ask-password request with it.
//
// The wire protocol always sends a ConfirmCodeChallenge right after
// Hello, whether or not confirmCode is set: an empty Code means "not
// required". A key holder that recognises the empty code skips both its
// own console prompt and sending a response back, and this function
// mirrors that by only waiting for a ConfirmCodeResponse when confirmCode
// is non-empty. This lets the two message types be told apart by their
// fixed position in the exchange, without adding a discriminator field to
// mrcore/wire.go.
func runAgentOnce(ctx context.Context, device string, machineCert tls.Certificate, machineName string, confirmCode string, askPasswordDir string, listenOpts transport.ListenOptions, tr transport.Transport) error {
	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		return fmt.Errorf("unlocker: reading LUKS header: %w", err)
	}

	tok, existingTokenID, err := loadToken(string(dump), device, machineName, "")
	if err != nil {
		return fmt.Errorf("unlocker: loading token: %w", err)
	}
	if existingTokenID < 0 {
		return fmt.Errorf("unlocker: %s has no enrolled mr-1 recipients", device)
	}

	var authorizedPeers []string
	for _, rec := range tok.Recipients {
		if id, ok := rec.TransportDeviceID(); ok {
			authorizedPeers = append(authorizedPeers, id)
		}
	}

	opts := listenOpts
	opts.AuthorizedPeers = authorizedPeers

	conn, err := tr.Listen(ctx, opts)
	if err != nil {
		return fmt.Errorf("unlocker: listening: %w", err)
	}
	defer conn.Close()

	return runExchange(ctx, conn, tok, device, machineName, confirmCode, askPasswordDir)
}

// runExchange is everything that happens once a key holder is on the
// other end of conn: greet it, put the confirm code to it if there is
// one, ask it to answer a challenge, check what comes back actually
// opens the volume, hand that to systemd, and tell the key holder how
// it went.
//
// Split out from runAgentOnce because the two halves fail for quite
// different reasons and want reading separately: everything above is
// this machine getting ready and finding somebody to talk to, and
// everything here is a conversation with a party that may be lying.
func runExchange(
	ctx context.Context,
	conn transport.Conn,
	tok mrcore.Token,
	device, machineName, confirmCode, askPasswordDir string,
) error {
	// Bound the exchange itself, not just the wait for a connection.
	//
	// Every read below is a blocking read with no deadline of its own,
	// so a key holder that connects and then stops talking holds the
	// machine for the rest of the boot: the agent handles one
	// connection at a time, so nobody else can unlock it either, and
	// the person who could fix it is by definition not standing next
	// to it. No malice is needed. A phone that loses signal between
	// dialling and answering, or an app killed while the biometric
	// prompt is up, leaves exactly this.
	//
	// Closing the connection is what unblocks a read, there being no
	// deadline on the Conn interface; the agent's outer loop then
	// starts a fresh attempt. The budget is generous because the
	// exchange legitimately waits for a human to approve on a phone,
	// and in confirm-code mode to read a code off this console and
	// type it in.
	exchangeCtx, cancelExchange := context.WithTimeout(ctx, exchangeTimeout)
	defer cancelExchange()
	// Closing the connection is what unblocks a read; context.AfterFunc
	// arranges that for when the budget runs out, and the stop it
	// returns unwinds the arrangement when the exchange finishes first.
	defer context.AfterFunc(exchangeCtx, func() { conn.Close() })()

	var matched *mrcore.Recipient
	for i := range tok.Recipients {
		if id, ok := tok.Recipients[i].TransportDeviceID(); ok && id == conn.PeerID() {
			matched = &tok.Recipients[i]
			break
		}
	}
	if matched == nil {
		return fmt.Errorf("unlocker: connected peer %s does not match any enrolled recipient", conn.PeerID())
	}

	if err := mrcore.WriteMessage(conn, mrcore.Hello{Name: machineName}); err != nil {
		return fmt.Errorf("unlocker: sending hello: %w", err)
	}

	if err := mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: confirmCode}); err != nil {
		return fmt.Errorf("unlocker: sending confirm code challenge: %w", err)
	}
	if confirmCode != "" {
		var resp mrcore.ConfirmCodeResponse
		if err := mrcore.ReadMessage(conn, &resp); err != nil {
			return fmt.Errorf("unlocker: reading confirm code response: %w", err)
		}
		if !confirmCodeMatches(resp.Code, confirmCode) {
			return fmt.Errorf("unlocker: wrong confirm code from %s", conn.PeerID())
		}
	}

	e, X, err := mrcore.ChallengeStart(mrcore.P256(), matched.C)
	if err != nil {
		return fmt.Errorf("unlocker: starting challenge: %w", err)
	}
	defer mrcore.Wipe(e)

	if err := mrcore.WriteMessage(conn, mrcore.RecoverRequest{Kid: matched.Kid, X: X}); err != nil {
		return fmt.Errorf("unlocker: sending recover request: %w", err)
	}

	var resp mrcore.RecoverResponse
	if err := mrcore.ReadMessage(conn, &resp); err != nil {
		return fmt.Errorf("unlocker: reading recover response: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("unlocker: key holder declined: %s", resp.Error)
	}

	// From here on the key holder has done its part and is waiting to
	// be told what came of it. Every exit below reports the outcome
	// before returning, because a key holder that only knows its own
	// write succeeded cannot distinguish an unlocked machine from an
	// answer that vanished into a dead relay session: the Android app
	// told a human their machine had unlocked while it sat at its
	// prompt, on exactly that basis.
	//
	// Best effort, deliberately: if the key holder has gone, the
	// machine's own outcome is unchanged and the write failing must
	// not mask it.
	report := func(outcome error) error {
		result := mrcore.RecoverResult{OK: outcome == nil}
		if outcome != nil {
			result.Error = outcome.Error()
		}
		_ = mrcore.WriteMessage(conn, result)
		// Writing is not delivering. Closing immediately after the
		// write tears down the relay control connection, and the relay
		// server drops every session belonging to a device the instant
		// that happens, so the verdict can be discarded in transit by
		// the very act of finishing with the connection. Seen exactly
		// that way: the machine unlocked and said so, and the key
		// holder was told the machine never reported an outcome.
		//
		// So wait for the key holder to close first, which it does as
		// soon as it has read the verdict, and give up after a moment
		// if it does not: this is the last thing either side has to
		// say, and the unlock has already happened regardless.
		waitForPeerClose(conn, resultLingerTimeout)
		return outcome
	}

	var P []byte
	switch {
	case len(resp.Y) > 0:
		P, err = mrcore.FinishFullPoint(mrcore.P256(), e, *matched, resp.Y)
	case len(resp.XOnly) > 0:
		P, err = mrcore.FinishXOnly(mrcore.P256(), e, *matched, resp.XOnly)
	default:
		return report(fmt.Errorf("unlocker: recover response carries neither Y nor XOnly"))
	}
	if err != nil {
		return report(fmt.Errorf("unlocker: finishing recovery: %w", err))
	}
	defer mrcore.Wipe(P)

	// keyMaterial, not P itself, is what actually unlocked the keyslot
	// (enrolRecipient adds the hex form, never the raw secret) and
	// what gets delivered: see luksKeyMaterial's own comment for why a
	// raw random secret cannot go through systemd's ask-password
	// protocol safely.
	keyMaterial := luksKeyMaterial(P)
	defer mrcore.Wipe(keyMaterial)

	if err := verifyPassphrase(device, keyMaterial); err != nil {
		return report(fmt.Errorf("unlocker: recovered secret does not open %s: %w", device, err))
	}

	if err := answerAskPassword(askPasswordDir, keyMaterial); err != nil {
		return report(fmt.Errorf("unlocker: answering ask-password request: %w", err))
	}

	return report(nil)
}

// confirmCodeMatches compares a relayed confirm code against the real
// one without leaking, through how long it takes, how much of it was
// right.
//
// The code exists for one situation: somebody has the key holder but
// cannot see this machine's console, which is to say a stolen or
// borrowed phone. That is exactly the attacker who gets to make
// repeated attempts, since the same code is reused for every
// connection during one agent run, and a plain string comparison
// stops at the first differing byte. Six digits guessed blind is a
// million tries; six digits guessed a character at a time is sixty.
func confirmCodeMatches(got, want string) bool {
	// Comparing lengths first only leaks the length, which is not
	// secret: the code is always the same number of digits.
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// exchangeTimeout bounds one recovery exchange once a key holder has
// connected. Long enough for somebody to pick up a phone, approve, and
// authenticate, and in confirm-code mode to read six digits off a
// console and type them; short enough that a key holder which has
// silently gone away costs one attempt rather than the whole boot.
// A var rather than a const so tests can shorten it: the behaviour
// worth testing is that the agent gives up at all, and waiting three
// real minutes to watch it happen would make the suite slower than it
// is useful.
var exchangeTimeout = 3 * time.Minute

// resultLingerTimeout bounds how long the agent waits for the key
// holder to finish reading its verdict before closing regardless.
const resultLingerTimeout = 3 * time.Second

// waitForPeerClose blocks until the peer closes the connection or
// timeout elapses, whichever is first.
//
// The read is expected to fail: what is being waited for is the peer
// going away, which is how it signals that it has what it needs. The
// goroutine outlives this function when the timeout wins, and is
// released by the caller's own Close.
func waitForPeerClose(conn io.Reader, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// verifyPassphrase checks that p actually opens device before it is ever
// handed to systemd. Without this a wrong-looking P is indistinguishable
// from a right one until systemd-cryptsetup itself rejects it, by which
// time the key holder has already been told the transfer succeeded
// because from its side it did; see syncthing-socket's luks_agent.go
// verifyPassphrase for the same reasoning against a plain string
// passphrase rather than a raw LUKS key.
func verifyPassphrase(device string, p []byte) error {
	// On its own descriptor, like the other secrets. This one's length
	// is a constant (hex of a fixed-size secret) so it disclosed
	// nothing, but there is no reason for the recovered key material
	// to be handled differently from the operator's passphrase.
	_, err := runCommandWithSecrets([][]byte{p}, "cryptsetup",
		func(paths []string) []string {
			return []string{"open", "--test-passphrase", "--key-file=" + paths[0], device}
		})
	return err
}

// askRequest is one entry in /run/systemd/ask-password, as documented at
// https://systemd.io/PASSWORD_AGENTS/: an ini-ish file naming a Unix
// datagram socket to reply to.
type askRequest struct {
	socket   string
	id       string
	notAfter uint64 // CLOCK_MONOTONIC microseconds; 0 means never expires
}

// parseAskFile reads one ask.XXXXXX file. It is not a full ini parser on
// purpose: systemd writes this file itself, so only comments, the section
// header and key=value lines are worth handling.
func parseAskFile(r io.Reader) (askRequest, error) {
	var req askRequest
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Socket":
			req.socket = strings.TrimSpace(value)
		case "Id":
			req.id = strings.TrimSpace(value)
		case "NotAfter":
			n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return askRequest{}, fmt.Errorf("unparseable NotAfter %q: %w", value, err)
			}
			req.notAfter = n
		}
	}
	if err := scanner.Err(); err != nil {
		return askRequest{}, err
	}
	if req.socket == "" {
		return askRequest{}, fmt.Errorf("no Socket= in the request")
	}
	return req, nil
}

func (a askRequest) wantsCryptsetup() bool {
	return strings.HasPrefix(a.id, cryptsetupAskIDPrefix)
}

func (a askRequest) expired(nowUsec uint64) bool {
	return a.notAfter != 0 && nowUsec >= a.notAfter
}

// monotonicNowUsec approximates CLOCK_MONOTONIC, the clock systemd's
// NotAfter is expressed in, from /proc/uptime, which tracks time since
// boot closely enough for this purpose without needing a syscall wrapper.
func monotonicNowUsec() (uint64, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("unexpected /proc/uptime format")
	}
	var seconds float64
	if _, err := fmt.Sscanf(fields[0], "%f", &seconds); err != nil {
		return 0, fmt.Errorf("parsing /proc/uptime: %w", err)
	}
	return uint64(seconds * 1e6), nil
}

// findCryptsetupAskRequest scans dir for the first pending cryptsetup
// password request: a file named ask.* whose Id starts with "cryptsetup"
// and which has not already expired. The NotAfter check is skipped
// entirely when it is zero (never expires), which is also all a fake
// test directory needs to exercise.
func findCryptsetupAskRequest(dir string) (askRequest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return askRequest{}, fmt.Errorf("reading %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "ask.") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		req, err := parseAskFile(f)
		f.Close()
		if err != nil {
			continue
		}
		if !req.wantsCryptsetup() {
			continue
		}
		if req.notAfter != 0 {
			now, err := monotonicNowUsec()
			if err == nil && req.expired(now) {
				continue
			}
		}
		return req, nil
	}
	return askRequest{}, fmt.Errorf("no pending cryptsetup password request in %s", dir)
}

// answerAskPassword finds the pending cryptsetup request in dir and
// replies with p, prefixed with "+" per systemd's password agent
// protocol. The reply is a connect-and-send on an unbound datagram
// socket: nothing is bound locally, so no stray socket file is left
// behind in what may be an initramfs.
func answerAskPassword(dir string, p []byte) error {
	req, err := findCryptsetupAskRequest(dir)
	if err != nil {
		return err
	}

	conn, err := net.Dial("unixgram", req.socket)
	if err != nil {
		return fmt.Errorf("dialing %s: %w", req.socket, err)
	}
	defer conn.Close()

	payload := append([]byte("+"), p...)
	defer mrcore.Wipe(payload)

	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("writing to %s: %w", req.socket, err)
	}
	return nil
}
