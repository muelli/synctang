// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"

	"github.com/muelli/synctang/internal/ui"
	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
)

// runUnlockOnce dials machineID and walks the MR-1 recovery exchange with
// it as the file-backed key holder: it always answers with the full point
// Y, computed directly from s, never XOnly (XOnly is only for
// hardware-backed key holders that cannot expose s in the clear, such as
// Android Keystore or a TPM2; this backend holds s directly). The caller
// owns wiping s: this may be called more than once against the same s
// (see runUnlock's retry), so wiping it here after the first call would
// corrupt every attempt after that.

// newDialTransport builds the Transport runUnlockOnce dials through.
// An explicit directAddr bypasses discovery entirely (offline tests,
// and manual same-network pairing), exactly as before; otherwise
// LocalDiscovery and SyncthingRelay are raced together
// (transport.Multi), so unlock keeps working on a LAN with no
// Internet route without the caller having to know in advance which
// situation it is in.
//
// network chooses the discovery server the machine is looked up on
// (and the relay pool, which a dialer does not use: it joins the relay
// the machine announced); its zero value is the public Syncthing ones.
func newDialTransport(cert tls.Certificate, directAddr string, network transport.NetworkConfig) transport.Transport {
	relay := network.NewSyncthingRelay(cert)
	if directAddr != "" {
		return relay
	}
	return &transport.Multi{Transports: []transport.Transport{
		&transport.LocalDiscovery{Cert: cert},
		relay,
	}}
}

// dialError is a failure to reach the machine at all, as opposed to one partway through
// talking to it: only that is worth asking discovery about.
type dialError struct{ error }

func (e *dialError) Unwrap() error { return e.error }

func runUnlockOnce(ctx context.Context, s []byte, cert tls.Certificate, machineID, alias, directAddr string, network transport.NetworkConfig, yes bool, stdin io.Reader, stdout io.Writer, quiet func()) error {
	tr := newDialTransport(cert, directAddr, network)
	conn, err := tr.Dial(ctx, transport.DialOptions{PeerID: machineID, DirectAddr: directAddr})
	if err != nil {
		return &dialError{fmt.Errorf("dialing %s: %w", machineID, err)}
	}
	defer conn.Close()

	var hello mrcore.Hello
	if err := mrcore.ReadMessage(conn, &hello); err != nil {
		return fmt.Errorf("reading hello: %w", err)
	}

	in := bufio.NewReader(stdin)

	quiet() // whatever was shown while waiting goes before the next thing is printed
	if !yes {
		fmt.Fprintf(stdout, "%s wants to unlock. Press Enter to approve, Ctrl-C to abort: ", describeMachine(alias, hello.Name, machineID))
		if _, err := in.ReadString('\n'); err != nil && err != io.EOF {
			return fmt.Errorf("reading approval: %w", err)
		}
	}

	var req mrcore.RecoverRequest
	if err := mrcore.ReadMessage(conn, &req); err != nil {
		return fmt.Errorf("reading recover request: %w", err)
	}

	// The machine chose this point, so check it before multiplying the
	// long-term secret by it.
	if err := mrcore.ValidateChallengePoint(mrcore.P256(), req.X); err != nil {
		return fmt.Errorf("refusing the machine's challenge: %w", err)
	}

	Y, err := mrcore.P256().ScalarMult(req.X, s)
	if err != nil {
		return fmt.Errorf("computing response: %w", err)
	}

	if err := mrcore.WriteMessage(conn, mrcore.RecoverResponse{Y: Y}); err != nil {
		return fmt.Errorf("sending recover response: %w", err)
	}

	// Wait to be told what came of it. "unlocked" used to be printed
	// on the strength of that write succeeding, which is not the same
	// claim: a relay session that has quietly died accepts a write and
	// delivers it nowhere.
	var result mrcore.RecoverResult
	if err := mrcore.ReadMessage(conn, &result); err != nil {
		return fmt.Errorf("answer sent, but the machine did not report the outcome, so it may or may not have unlocked: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("the machine could not use the answer: %s", result.Error)
	}

	look := ui.For(stdout)
	name := ""
	if alias != "" {
		name = " " + look.Dim(alias)
	}
	fmt.Fprintln(stdout, look.Line(look.OK(""), look.Green(look.Emoji("🔓", "")+"unlocked")+name))
	return nil
}

// describeMachine is how the approval prompt names the machine. The identity is what was dialled and is
// always shown. alias is the name the person gave the machine in the address book (empty if they dialled
// an id); announced is what the machine says it is called, which proves nothing and is cleaned of control
// characters, and is shown when there is no alias or when it differs from it. Nothing is shown for a name
// that is empty, rather than a pair of quotes.
func describeMachine(alias, announced, id string) string {
	announced = mrcore.SafeLabel(announced, 64)
	switch {
	case alias != "" && (announced == "" || announced == alias):
		return fmt.Sprintf("Machine %s (%s)", alias, id)
	case alias != "":
		return fmt.Sprintf("Machine %s (calls itself %q, %s)", alias, announced, id)
	case announced != "":
		return fmt.Sprintf("Machine %q (%s)", announced, id)
	}
	return "Machine " + id
}
