// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
		if resp.Code != confirmCode {
			return fmt.Errorf("unlocker: wrong confirm code from %s", conn.PeerID())
		}
	}

	e, X, err := mrcore.ChallengeStart(mrcore.P256(), matched.C)
	if err != nil {
		return fmt.Errorf("unlocker: starting challenge: %w", err)
	}
	defer wipe(e)

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

	var P []byte
	switch {
	case len(resp.Y) > 0:
		P, err = mrcore.FinishFullPoint(mrcore.P256(), e, *matched, resp.Y)
	case len(resp.XOnly) > 0:
		P, err = mrcore.FinishXOnly(mrcore.P256(), e, *matched, resp.XOnly)
	default:
		return fmt.Errorf("unlocker: recover response carries neither Y nor XOnly")
	}
	if err != nil {
		return fmt.Errorf("unlocker: finishing recovery: %w", err)
	}
	defer wipe(P)

	// keyMaterial, not P itself, is what actually unlocked the keyslot
	// (enrolRecipient adds the hex form, never the raw secret) and
	// what gets delivered: see luksKeyMaterial's own comment for why a
	// raw random secret cannot go through systemd's ask-password
	// protocol safely.
	keyMaterial := luksKeyMaterial(P)
	defer wipe(keyMaterial)

	if err := verifyPassphrase(device, keyMaterial); err != nil {
		return fmt.Errorf("unlocker: recovered secret does not open %s: %w", device, err)
	}

	if err := answerAskPassword(askPasswordDir, keyMaterial); err != nil {
		return fmt.Errorf("unlocker: answering ask-password request: %w", err)
	}

	return nil
}

// verifyPassphrase checks that p actually opens device before it is ever
// handed to systemd. Without this a wrong-looking P is indistinguishable
// from a right one until systemd-cryptsetup itself rejects it, by which
// time the key holder has already been told the transfer succeeded
// because from its side it did; see syncthing-socket's luks_agent.go
// verifyPassphrase for the same reasoning against a plain string
// passphrase rather than a raw LUKS key.
func verifyPassphrase(device string, p []byte) error {
	_, err := runCommand(p, "cryptsetup", "open", "--test-passphrase",
		"--key-file=-", fmt.Sprintf("--keyfile-size=%d", len(p)), device)
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
	defer wipe(payload)

	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("writing to %s: %w", req.socket, err)
	}
	return nil
}
