// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"strings"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
)

// runUnlockOnce dials machineID and walks the MR-1 recovery exchange with
// it as the file-backed key holder: it always answers with the full point
// Y, computed directly from s, never XOnly (XOnly is only for
// hardware-backed key holders that cannot expose s in the clear, such as
// Android Keystore or a TPM2; this backend holds s directly). s is wiped
// before returning.
//
// The machine always sends a ConfirmCodeChallenge right after Hello,
// whether or not it was started with --confirm-code: an empty Code means
// "not required", which this function recognises by skipping both the
// console prompt for it and sending back a ConfirmCodeResponse at all.
// That is how the two message types are told apart, by their fixed
// position in the exchange rather than by a discriminator field in
// mrcore/wire.go; cmd/unlocker's agent.go follows the same convention.
func runUnlockOnce(ctx context.Context, s []byte, cert tls.Certificate, machineID, directAddr string, yes bool, stdin io.Reader, stdout io.Writer) error {
	defer wipe(s)

	tr := transport.NewSyncthingRelay(cert)
	conn, err := tr.Dial(ctx, transport.DialOptions{PeerID: machineID, DirectAddr: directAddr})
	if err != nil {
		return fmt.Errorf("dialing %s: %w", machineID, err)
	}
	defer conn.Close()

	var hello mrcore.Hello
	if err := mrcore.ReadMessage(conn, &hello); err != nil {
		return fmt.Errorf("reading hello: %w", err)
	}

	in := bufio.NewReader(stdin)

	if !yes {
		fmt.Fprintf(stdout, "Machine %q (%s) wants to unlock. Press Enter to approve, Ctrl-C to abort: ", hello.Name, machineID)
		if _, err := in.ReadString('\n'); err != nil && err != io.EOF {
			return fmt.Errorf("reading approval: %w", err)
		}
	}

	var challenge mrcore.ConfirmCodeChallenge
	if err := mrcore.ReadMessage(conn, &challenge); err != nil {
		return fmt.Errorf("reading confirm code challenge: %w", err)
	}
	if challenge.Code != "" {
		fmt.Fprint(stdout, "The machine's console should be showing a code. Enter it: ")
		line, err := in.ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("reading confirm code: %w", err)
		}
		if err := mrcore.WriteMessage(conn, mrcore.ConfirmCodeResponse{Code: strings.TrimSpace(line)}); err != nil {
			return fmt.Errorf("sending confirm code response: %w", err)
		}
	}

	var req mrcore.RecoverRequest
	if err := mrcore.ReadMessage(conn, &req); err != nil {
		return fmt.Errorf("reading recover request: %w", err)
	}

	Y, err := mrcore.P256().ScalarMult(req.X, s)
	if err != nil {
		return fmt.Errorf("computing response: %w", err)
	}

	if err := mrcore.WriteMessage(conn, mrcore.RecoverResponse{Y: Y}); err != nil {
		return fmt.Errorf("sending recover response: %w", err)
	}

	fmt.Fprintln(stdout, "unlocked")
	return nil
}
