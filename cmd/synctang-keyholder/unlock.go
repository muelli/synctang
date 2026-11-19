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
		return &dialError{fmt.Errorf(T("dialing %s: %w"), machineID, err)}
	}
	defer conn.Close()

	var hello mrcore.Hello
	if err := mrcore.ReadMessage(conn, &hello); err != nil {
		return fmt.Errorf(T("reading hello: %w"), err)
	}

	in := bufio.NewReader(stdin)

	quiet() // whatever was shown while waiting goes before the next thing is printed
	who := describeMachine(alias, hello.Name, machineID)
	// What the machine says it will do with the answer. The answer is the passphrase of this key holder's own
	// keyslot, and what the machine does with it is the machine's choice: the notice is true of every approval, and
	// a machine that says it will do something other than unlock is said to, as a warning, whatever --yes says.
	fmt.Fprintln(stdout, approvalNotice(hello.Purpose, who))
	if !yes {
		verb := T("wants to unlock")
		if hello.Purpose != "" {
			verb = T("asks for an approval")
		}
		fmt.Fprintf(stdout, T("%s %s. Press Enter to approve, Ctrl-C to abort: "), who, verb)
		if _, err := in.ReadString('\n'); err != nil && err != io.EOF {
			return fmt.Errorf(T("reading approval: %w"), err)
		}
	}

	var req mrcore.RecoverRequest
	if err := mrcore.ReadMessage(conn, &req); err != nil {
		return fmt.Errorf(T("reading recover request: %w"), err)
	}

	// The machine chose this point, so check it before multiplying the
	// long-term secret by it.
	if err := mrcore.ValidateChallengePoint(mrcore.P256(), req.X); err != nil {
		return fmt.Errorf(T("refusing the machine's challenge: %w"), err)
	}

	Y, err := mrcore.P256().ScalarMult(req.X, s)
	if err != nil {
		return fmt.Errorf(T("computing response: %w"), err)
	}

	if err := mrcore.WriteMessage(conn, mrcore.RecoverResponse{Y: Y}); err != nil {
		return fmt.Errorf(T("sending recover response: %w"), err)
	}

	// Wait to be told what came of it. "unlocked" used to be printed
	// on the strength of that write succeeding, which is not the same
	// claim: a relay session that has quietly died accepts a write and
	// delivers it nowhere.
	var result mrcore.RecoverResult
	if err := mrcore.ReadMessage(conn, &result); err != nil {
		return fmt.Errorf(T("answer sent, but the machine did not report the outcome, so it may or may not have unlocked: %w"), err)
	}
	if !result.OK {
		return fmt.Errorf(T("the machine could not use the answer: %s"), result.Error)
	}

	look := ui.For(stdout)
	name := ""
	if alias != "" {
		name = " " + look.Dim(alias)
	}
	if hello.Purpose != "" {
		// Not "unlocked": the machine said it would not unlock, and what it did with the answer is its own.
		fmt.Fprintln(stdout, look.Line(look.OK(""), look.Green(look.Emoji("🔓", "")+fmt.Sprintf(T("approved (%s)"), purposeAction(hello.Purpose)))+name))
		return nil
	}
	fmt.Fprintln(stdout, look.Line(look.OK(""), look.Green(look.Emoji("🔓", "")+"unlocked")+name))
	return nil
}

// purposeAction is what a machine's purpose (mrcore.Hello.Purpose) means in words, as a verb phrase. A purpose
// this program does not know is shown as the machine put it, cleaned: it is still something other than an unlock.
func purposeAction(purpose string) string {
	switch purpose {
	case mrcore.PurposeEnrol:
		return T("enrol a key holder")
	case mrcore.PurposePair:
		return T("pair a key holder")
	case mrcore.PurposeExtract:
		return T("print the passphrase of this key holder's keyslot")
	}
	return fmt.Sprintf(T("do something it calls %q"), mrcore.SafeLabel(purpose, 64))
}

// approvalNotice is said before every approval. For an unlock it is the standing notice: approving gives the
// machine the passphrase of this key holder's keyslot. For anything else it is a warning that says what the machine
// says it will do instead. Either way it is the machine's own word that is reported, and an honest machine's
// advice: a machine that does something it did not say looks exactly like an unlock.
func approvalNotice(purpose, who string) string {
	standing := T("Approving gives the machine the passphrase of this key holder's keyslot; with it the machine can open the volume and add or remove keys.")
	if purpose == "" {
		return standing
	}
	return fmt.Sprintf(T("This is not an unlock: %s says it will %s. "), who, purposeAction(purpose)) + standing
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
