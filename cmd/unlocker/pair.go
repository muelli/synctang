// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// pairNonceLen is the length of the per-connection nonce a key holder
// binds its proof to.
const pairNonceLen = 32

// pairOptions is everything one pairing window needs. Split out from
// the flags so the exchange can be tested without a console, a QR code
// or a human.
type pairOptions struct {
	device             string
	existingPassphrase []byte
	machineCert        tls.Certificate
	machineName        string
	secret             []byte

	// confirm is asked before anything is written to the volume,
	// with the key holder's verified Device ID and the public key it
	// wants enrolled. Returning false abandons the pairing.
	confirm func(peerID string, pubKey []byte) bool

	listenOpts transport.ListenOptions
	logger     *slog.Logger
}

// runPairOnce accepts exactly one pairing attempt and returns when it
// has been resolved, one way or the other.
//
// The listener deliberately accepts any peer: a key holder that is not
// enrolled yet is precisely what pairing exists to enrol, so there is
// no list to check it against. What replaces that check is the proof
// below, which requires the secret from the machine's own screen, plus
// the operator's confirmation before any keyslot is written. A stranger
// who reaches this port therefore gets a challenge, a rejection, and
// nothing else.
func runPairOnce(ctx context.Context, opts pairOptions) error {
	logger := opts.logger
	if logger == nil {
		logger = slog.Default()
	}

	machineID := protocol.NewDeviceID(opts.machineCert.Certificate[0]).String()

	tr := newAgentTransport(opts.machineCert, logger, opts.listenOpts.DirectAddr)
	listenOpts := opts.listenOpts
	listenOpts.AuthorizedPeers = nil

	conn, err := tr.Listen(ctx, listenOpts)
	if err != nil {
		return fmt.Errorf("unlocker: waiting for a key holder to pair: %w", err)
	}
	defer conn.Close()

	nonce := make([]byte, pairNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("unlocker: generating a pairing nonce: %w", err)
	}
	if err := mrcore.WriteMessage(conn, mrcore.PairChallenge{Nonce: nonce, Name: opts.machineName}); err != nil {
		return fmt.Errorf("unlocker: sending the pairing challenge: %w", err)
	}

	var req mrcore.PairRequest
	if err := mrcore.ReadMessage(conn, &req); err != nil {
		return fmt.Errorf("unlocker: reading the pairing request: %w", err)
	}

	// PeerID is the identity from the completed TLS handshake, not
	// anything this message claimed, so the proof is bound to who the
	// peer actually is and the enrolment records that same identity.
	peerID := conn.PeerID()
	expected := mrcore.PairProof(opts.secret, machineID, peerID, nonce)
	if !hmac.Equal(expected, req.Proof) {
		logger.Warn("refused a pairing attempt with a bad proof", "peer", peerID)
		return refusePairing(conn, "the pairing code did not match")
	}

	// Rejected here rather than being left for enrolRecipient, so a
	// key that can never work does not first get a human asked about
	// it. PointToX decodes the point, which is the validation wanted:
	// on the curve, in the right subgroup, not the point at infinity.
	if _, err := mrcore.P256().PointToX(req.PubKey); err != nil {
		return refusePairing(conn, "the public key offered is not a valid P-256 point")
	}

	if opts.confirm != nil && !opts.confirm(peerID, req.PubKey) {
		return refusePairing(conn, "the operator declined the pairing")
	}

	if err := enrolRecipient(opts.device, opts.existingPassphrase, req.PubKey,
		opts.machineName, machineID, peerID); err != nil {
		// The key holder is waiting, and "it failed" is worth far more
		// to whoever is holding the phone than a timeout.
		_ = mrcore.WriteMessage(conn, mrcore.PairResult{Error: "the machine could not enrol this key holder"})
		waitForPeerClose(conn, resultLingerTimeout)
		return fmt.Errorf("unlocker: enrolling the paired key holder: %w", err)
	}

	if err := mrcore.WriteMessage(conn, mrcore.PairResult{OK: true}); err != nil {
		// The enrolment happened and stands; only the acknowledgement
		// was lost, so say so rather than implying nothing changed.
		return fmt.Errorf("unlocker: %s was enrolled, but telling it so failed: %w", peerID, err)
	}
	waitForPeerClose(conn, resultLingerTimeout)
	return nil
}

// refusePairing tells the peer why, then reports the same thing to the
// caller. The reason is not withheld: whoever is dialling already knows
// whether they hold the code, and a person who mistyped it off a
// console needs to be told that rather than left guessing.
func refusePairing(conn io.ReadWriter, reason string) error {
	_ = mrcore.WriteMessage(conn, mrcore.PairResult{Error: reason})
	waitForPeerClose(conn, resultLingerTimeout)
	return fmt.Errorf("unlocker: pairing refused: %s", reason)
}

// defaultPairWindow is how long a pairing code stays good for. Long
// enough to walk to the machine and unlock a phone, short enough that a
// code left on a screen is not an open door.
const defaultPairWindow = 5 * time.Minute

// runPair is the operator-facing command: show a code, wait for a key
// holder to answer it, enrol it.
//
// Deliberately a command a person runs and watches, not a service. It
// needs the volume's existing passphrase, it lasts minutes, it accepts
// one key holder, and it shows who is asking before writing anything.
func runPair(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(stderr)
	device := fs.String("device", "", "LUKS2 device or image to enrol the key holder on")
	passphraseFile := fs.String("existing-passphrase-file", "", "file containing an existing valid LUKS passphrase (- for stdin)")
	name := fs.String("name", "", "this machine's name, shown to the key holder")
	machineKeyFile := fs.String("machine-key-file", defaultMachineKeyFile, "path to this machine's persistent transport identity")
	window := fs.Duration("timeout", defaultPairWindow, "how long to accept a pairing for")
	yes := fs.Bool("yes", false, "enrol whoever answers the code without asking for confirmation")
	directAddr := fs.String("direct-addr", "", "listen on this address directly, bypassing relay and discovery (testing only)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *device == "" || *passphraseFile == "" {
		fmt.Fprintln(stderr, "unlocker: --device and --existing-passphrase-file are required")
		return 2
	}

	passphrase, err := readPassphrase(*passphraseFile)
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: %v\n", err)
		return 1
	}
	defer mrcore.Wipe(passphrase)

	cert, err := transport.LoadOrCreateCert(*machineKeyFile)
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: %v\n", err)
		return 1
	}
	machineID := protocol.NewDeviceID(cert.Certificate[0]).String()

	secret, encoded, err := mrcore.NewPairingSecret()
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: %v\n", err)
		return 1
	}
	defer mrcore.Wipe(secret)

	if qrCode, err := renderPairingQRCode(pairingURL(machineID, *name, encoded)); err != nil {
		// Not fatal: the printed values below are enough to pair by
		// hand, and a machine whose terminal cannot show a QR code is
		// exactly the case they exist for.
		fmt.Fprintf(stderr, "unlocker: could not draw the QR code (%v); use the values below\n", err)
	} else {
		fmt.Fprintln(stdout)
		fmt.Fprint(stdout, qrCode)
		fmt.Fprintln(stdout)
	}
	fmt.Fprint(stdout, pairingInstructions(machineID, *name, encoded, *window))
	fmt.Fprintln(stdout)

	ctx, cancel := context.WithTimeout(context.Background(), *window)
	defer cancel()

	opts := pairOptions{
		device:             *device,
		existingPassphrase: passphrase,
		machineCert:        cert,
		machineName:        *name,
		secret:             secret,
		confirm:            pairConfirmer(*yes, stdout, os.Stdin),
		listenOpts:         transport.ListenOptions{DirectAddr: *directAddr},
	}

	// Retried rather than abandoned on the first refusal: a code read
	// off a console gets mistyped, and making that end the window means
	// starting over with a new code for no reason.
	for {
		err := runPairOnce(ctx, opts)
		if err == nil {
			fmt.Fprintln(stdout, "paired. The key holder can now unlock this volume.")
			return 0
		}
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "unlocker: nobody paired within %s\n", *window)
			return 1
		}
		fmt.Fprintf(stderr, "unlocker: %v (still waiting)\n", err)
	}
}

// pairConfirmer builds the confirmation step. Possession of the code is
// not by itself consent: the code is shown on a screen and can be
// photographed, so the operator standing at the machine is shown the
// identity that answered it and decides.
func pairConfirmer(assumeYes bool, stdout io.Writer, stdin io.Reader) func(string, []byte) bool {
	if assumeYes {
		return func(string, []byte) bool { return true }
	}
	reader := bufio.NewReader(stdin)
	return func(peerID string, pubKey []byte) bool {
		fmt.Fprintf(stdout, "\na key holder answered the pairing code:\n\n")
		fmt.Fprintf(stdout, "  transport id:  %s\n", peerID)
		fmt.Fprintf(stdout, "  public key:    %s\n\n", shortKid(pubKey))
		fmt.Fprint(stdout, "enrol it on this volume? [y/N] ")

		line, err := reader.ReadString('\n')
		if err != nil {
			// No console to ask, or stdin already spent on the
			// passphrase. Refusing is the safe reading of silence.
			fmt.Fprintln(stdout, "\nno answer, so not enrolling (use --yes to skip this question)")
			return false
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes"
	}
}

// renderPairingQRCode honours NO_COLOR (https://no-color.org/), which
// matters here beyond taste: the coloured form is built from ANSI
// escapes, and piping it into a file or a log turns it into unreadable
// noise, while the plain form survives.
func renderPairingQRCode(content string) (string, error) {
	if _, noColour := os.LookupEnv("NO_COLOR"); noColour {
		return renderQRCodePlain(content)
	}
	return renderQRCode(content)
}

// pairingURL is what the QR code encodes, and what the Android app
// already knows how to parse: the same synctang://pair payload used for
// scanning a machine's identity, with the one-time code added.
func pairingURL(machineID, name, encodedSecret string) string {
	q := url.Values{}
	q.Set("machine", machineID)
	if name != "" {
		q.Set("name", name)
	}
	q.Set("psk", encodedSecret)
	return "synctang://pair?" + q.Encode()
}

// pairingInstructions is the text shown alongside the QR code, and the
// whole of what a serial console or a too-small terminal leaves a
// person with, so it has to be sufficient on its own.
func pairingInstructions(machineID, name, encodedSecret string, window time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Scan this with the synctang app to pair it with %s.\n\n", cmp.Or(name, "this machine"))
	b.WriteString("Or enter these two values in the app by hand:\n\n")
	fmt.Fprintf(&b, "  Machine device ID:  %s\n", machineID)
	fmt.Fprintf(&b, "  Pairing code:       %s\n\n", encodedSecret)
	fmt.Fprintf(&b, "The pairing code is good once, for the next %s.\n", window)
	b.WriteString("Anyone who can read it off this screen, or off a photograph of\n")
	b.WriteString("it, can ask to be enrolled, so you will be shown which key\n")
	b.WriteString("holder is asking before anything is written to the volume.\n")
	return b.String()
}
