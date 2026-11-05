// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/muelli/synctang/internal/gnuflag"
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

// pairRequestTimeout is how long a peer that has completed the handshake
// gets to send its pairing request. The key holder answers at once, so a
// peer still silent after this is not going to; waiting longer only lets
// it hold the pairing window against a real phone.
var pairRequestTimeout = 30 * time.Second

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

	// confirmReplace is asked instead of confirm when the key holder's
	// transport id is already enrolled with a different key: it names both
	// keys, and returning true replaces the old entry with the new one. Nil
	// means no.
	confirmReplace func(peerID string, existing existingRecipient, newKid []byte) bool

	// notify tells the operator something that is not a question, such as
	// that the key holder was already enrolled. Nil means the log.
	notify func(message string)

	listenOpts transport.ListenOptions
	logger     *slog.Logger

	// network chooses the relay pool and discovery servers; the zero
	// value is the public ones.
	network transport.NetworkConfig
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

	tr := newAgentTransport(opts.machineCert, logger, opts.listenOpts.DirectAddr, opts.network)
	listenOpts := opts.listenOpts
	listenOpts.AuthorizedPeers = nil

	conn, err := tr.Listen(ctx, listenOpts)
	if err != nil {
		return fmt.Errorf("synctang-unlocker: waiting for a key holder to pair: %w", err)
	}
	defer conn.Close()

	nonce := make([]byte, pairNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("synctang-unlocker: generating a pairing nonce: %w", err)
	}
	if err := mrcore.WriteMessage(conn, mrcore.PairChallenge{Nonce: nonce, Name: opts.machineName}); err != nil {
		return fmt.Errorf("synctang-unlocker: sending the pairing challenge: %w", err)
	}

	// Closing the connection is what interrupts a blocked read: the
	// transport's Conn has no deadlines. Both the per-peer timeout and
	// the end of the whole pairing window do it, so a silent peer cannot
	// outlive either.
	requestTimer := time.AfterFunc(pairRequestTimeout, func() { conn.Close() })
	stopOnWindowEnd := context.AfterFunc(ctx, func() { conn.Close() })
	var req mrcore.PairRequest
	err = mrcore.ReadMessage(conn, &req)
	requestTimer.Stop()
	stopOnWindowEnd()
	if err != nil {
		return fmt.Errorf("synctang-unlocker: reading the pairing request: %w", err)
	}

	// PeerID is the identity from the completed TLS handshake, not
	// anything this message claimed, so the proof is bound to who the
	// peer actually is and the enrolment records that same identity.
	peerID := conn.PeerID()
	expected := mrcore.PairProof(opts.secret, machineID, peerID, req.PubKey, nonce)
	if !hmac.Equal(expected, req.Proof) {
		logger.Warn("refused a pairing attempt with a bad proof", "peer", peerID)
		return refusePairing(conn, mrcore.PairRefusedCodeMismatch)
	}

	// Rejected here rather than being left for enrolRecipient, so a
	// key that can never work does not first get a human asked about
	// it. PointToX decodes the point, which is the validation wanted:
	// on the curve, in the right subgroup, not the point at infinity.
	if _, err := mrcore.P256().PointToX(req.PubKey); err != nil {
		return refusePairing(conn, mrcore.PairRefusedInvalidKey)
	}

	// One enrolled recipient per transport id. The same key already there is
	// success with nothing to add; a different one is replaced, if the operator
	// agrees, by the existing rotation (new key first, then revoke the old, so
	// there is never a moment with neither).
	plan, err := planEnrolment(opts.device, req.PubKey, peerID)
	if err != nil {
		_ = mrcore.WriteMessage(conn, mrcore.PairResult{Error: mrcore.PairRefusedEnrolFailed})
		waitForPeerClose(conn, resultLingerTimeout)
		return fmt.Errorf("synctang-unlocker: %w", err)
	}
	notify := opts.notify
	if notify == nil {
		notify = func(m string) { logger.Info(m) }
	}
	switch plan.Kind {
	case planAlready:
		notify(fmt.Sprintf("%s is already enrolled at keyslot %s with this key; nothing was added", peerID, plan.Existing.Keyslot))
		if err := mrcore.WriteMessage(conn, mrcore.PairResult{OK: true}); err != nil {
			return fmt.Errorf("synctang-unlocker: %s was already enrolled, but telling it so failed: %w", peerID, err)
		}
		waitForPeerClose(conn, resultLingerTimeout)
		return nil
	case planReplace:
		newKid := sha256.Sum256(req.PubKey)
		if opts.confirmReplace == nil || !opts.confirmReplace(peerID, plan.Existing, newKid[:]) {
			notify("not replacing; nothing was changed")
			return refusePairing(conn, mrcore.PairRefusedDeclined)
		}
		if err := replaceRecipient(opts.device, opts.existingPassphrase, req.PubKey, peerID, peerID, plan.Existing.Keyslot); err != nil {
			_ = mrcore.WriteMessage(conn, mrcore.PairResult{Error: mrcore.PairRefusedEnrolFailed})
			waitForPeerClose(conn, resultLingerTimeout)
			return fmt.Errorf("synctang-unlocker: replacing the paired key holder's key: %w", err)
		}
		if err := mrcore.WriteMessage(conn, mrcore.PairResult{OK: true}); err != nil {
			return fmt.Errorf("synctang-unlocker: %s's key was replaced, but telling it so failed: %w", peerID, err)
		}
		waitForPeerClose(conn, resultLingerTimeout)
		return nil
	}

	if opts.confirm != nil && !opts.confirm(peerID, req.PubKey) {
		return refusePairing(conn, mrcore.PairRefusedDeclined)
	}

	if err := enrolRecipient(opts.device, opts.existingPassphrase, req.PubKey,
		opts.machineName, machineID, peerID); err != nil {
		// The key holder is waiting, and "it failed" is worth far more
		// to whoever is holding the phone than a timeout.
		_ = mrcore.WriteMessage(conn, mrcore.PairResult{Error: mrcore.PairRefusedEnrolFailed})
		waitForPeerClose(conn, resultLingerTimeout)
		return fmt.Errorf("synctang-unlocker: enrolling the paired key holder: %w", err)
	}

	if err := mrcore.WriteMessage(conn, mrcore.PairResult{OK: true}); err != nil {
		// The enrolment happened and stands; only the acknowledgement
		// was lost, so say so rather than implying nothing changed.
		return fmt.Errorf("synctang-unlocker: %s was enrolled, but telling it so failed: %w", peerID, err)
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
	return fmt.Errorf("synctang-unlocker: pairing refused: %s", reason)
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
	fs.SetOutput(gnuflag.Writer(stderr))
	device := fs.String("device", "", "LUKS2 device or image to enrol the key holder on (\"synctang-unlocker devices\" lists them)")
	passphraseFile := fs.String("existing-passphrase-file", "", "file containing an existing valid LUKS passphrase (- for stdin)")
	name := fs.String("name", "", "this machine's name, shown to the key holder (default: the name already on the volume, else this machine's hostname)")
	machineKeyFile := fs.String("machine-key-file", defaultMachineKeyFile, "path to this machine's persistent transport identity")
	window := fs.Duration("timeout", defaultPairWindow, "how long to accept a pairing for")
	yes := fs.Bool("yes", false, "enrol whoever answers the code without asking for confirmation")
	rebuild := fs.Bool("rebuild-initrd", false, "after enrolling, run dracut --force if the initrd does not carry this machine's identity and network.conf")
	directAddr := fs.String("direct-addr", "", "listen on this address directly, bypassing relay and discovery (testing only)")
	networkConf := fs.String("network-conf", defaultNetworkConfFile, "file naming the relay pool and discovery servers to use instead of the public ones (the kernel arguments rd.synctang.relay= and rd.synctang.discovery= override it)")
	qrMode := fs.String("qr", "auto", "how to draw the QR code: "+strings.Join(qrModes, ", ")+"; auto uses an image where the terminal is known to show one")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !validQRMode(*qrMode) {
		fmt.Fprintf(stderr, "synctang-unlocker: --qr: unknown mode %q, want one of %s\n", *qrMode, strings.Join(qrModes, ", "))
		return 2
	}

	if *device == "" {
		fmt.Fprintf(stderr, "synctang-unlocker: --device is required: %s\n", deviceHint())
		return 2
	}
	if *passphraseFile == "" {
		fmt.Fprintln(stderr, "synctang-unlocker: --existing-passphrase-file is required")
		return 2
	}
	// Found out now, before the passphrase is read and the window opens, not when a key
	// holder has already answered.
	answers, closeAnswers, err := confirmationInput(*passphraseFile, *yes, os.Stdin)
	if err != nil {
		fmt.Fprintf(stderr, "synctang-unlocker: %v\n", err)
		return 2
	}
	defer closeAnswers.Close()

	network, ok := resolveNetwork(*networkConf, stdout, stderr)
	if !ok {
		return 1
	}

	passphrase, err := readPassphrase(*passphraseFile)
	if err != nil {
		fmt.Fprintf(stderr, "synctang-unlocker: %v\n", err)
		return 1
	}
	defer mrcore.Wipe(passphrase)

	// Tested now, not left for the enrolment at the end: a typo would
	// otherwise show up only after the phone had scanned the code and the
	// person had confirmed it. Before the identity is made, too, so a
	// refused run leaves nothing behind.
	if err := verifyPassphrase(*device, passphrase); err != nil {
		fmt.Fprintf(stderr, "synctang-unlocker: that passphrase does not open %s (%v); nothing was changed\n", *device, err)
		return 1
	}

	cert, err := transport.LoadOrCreateCert(*machineKeyFile)
	if err != nil {
		fmt.Fprintf(stderr, "synctang-unlocker: %v\n", err)
		return 1
	}
	machineID := protocol.NewDeviceID(cert.Certificate[0]).String()
	*name = resolveMachineName(*name, existingMachineName(*device))

	secret, encoded, err := mrcore.NewPairingSecret()
	if err != nil {
		fmt.Fprintf(stderr, "synctang-unlocker: %v\n", err)
		return 1
	}
	defer mrcore.Wipe(secret)

	if qrCode, err := renderPairingQR(pairingURL(machineID, *name, encoded, network), *qrMode, os.Getenv, isTerminal(stdout)); err != nil {
		// Not fatal: the printed values below are enough to pair by
		// hand, and a machine whose terminal cannot show a QR code is
		// exactly the case they exist for.
		fmt.Fprintf(stderr, "synctang-unlocker: could not draw the QR code (%v); use the link below\n", err)
	} else {
		fmt.Fprintln(stdout)
		fmt.Fprint(stdout, qrCode)
		fmt.Fprintln(stdout)
	}
	fmt.Fprint(stdout, pairingInstructions(machineID, *name, encoded, network, *window))
	fmt.Fprintln(stdout)

	ctx, cancel := context.WithTimeout(context.Background(), *window)
	defer cancel()

	// One reader for both questions, so neither loses what the other buffered.
	stdin := bufio.NewReader(answers)

	opts := pairOptions{
		device:             *device,
		existingPassphrase: passphrase,
		machineCert:        cert,
		machineName:        *name,
		secret:             secret,
		confirm:            pairConfirmer(*yes, stdout, stdin),
		confirmReplace:     pairReplaceConfirmer(*yes, stdout, stdin),
		notify:             func(m string) { fmt.Fprintln(stdout, m) },
		listenOpts:         transport.ListenOptions{DirectAddr: *directAddr},
		network:            network,
	}

	// Retried rather than abandoned on the first refusal: a code read
	// off a console gets mistyped, and making that end the window means
	// starting over with a new code for no reason.
	for {
		err := runPairOnce(ctx, opts)
		if err == nil {
			fmt.Fprintln(stdout, "paired. The key holder can now unlock this volume.")
			// The first pairing on a machine makes its identity, which the initrd built
			// before then does not carry.
			return afterEnrolmentFor(stdout, stderr, *machineKeyFile, *networkConf, *rebuild)
		}
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "synctang-unlocker: nobody paired within %s\n", *window)
			return 1
		}
		fmt.Fprintf(stderr, "synctang-unlocker: %v (still waiting)\n", err)
	}
}

// openTTY opens the controlling terminal; a variable so that tests can supply one.
var openTTY = func() (io.ReadCloser, error) { return os.Open("/dev/tty") }

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// confirmationInput is where pair reads the operator's answers from. Usually that is stdin.
// With the passphrase on stdin ("--existing-passphrase-file -", so that it can come from a
// pipe and never be written to a disk) stdin is spent, so the questions are asked on the
// terminal, as ssh and sudo ask for theirs. With no terminal to ask on, the operator has to
// say --yes, which is reported at once, before the pairing window opens. With --yes nothing
// is asked, so nothing is needed.
func confirmationInput(passphraseFile string, assumeYes bool, stdin io.Reader) (io.Reader, io.Closer, error) {
	if passphraseFile != "-" || assumeYes {
		return stdin, nopCloser{}, nil
	}
	tty, err := openTTY()
	if err != nil {
		return nil, nil, fmt.Errorf("the passphrase is read from stdin, so the confirmation question has to be asked on a terminal, "+
			"and none could be opened (%v); use --yes to enrol whoever answers the code without asking", err)
	}
	return tty, tty, nil
}

// pairConfirmer builds the confirmation step. Possession of the code is
// not by itself consent: the code is shown on a screen and can be
// photographed, so the operator standing at the machine is shown the
// identity that answered it and decides.
//
// stdin should be one *bufio.Reader shared with pairReplaceConfirmer, so that
// whichever question is asked reads what was typed and nothing is buffered
// away from the other.
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

// pairReplaceConfirmer is the question asked when the key holder that
// answered the code is already enrolled with a different key: it names both
// keys, so the operator sees what is being replaced and by what. --yes means
// yes; silence means no.
func pairReplaceConfirmer(assumeYes bool, stdout io.Writer, stdin io.Reader) func(string, existingRecipient, []byte) bool {
	if assumeYes {
		return func(string, existingRecipient, []byte) bool { return true }
	}
	reader := bufio.NewReader(stdin)
	return func(peerID string, existing existingRecipient, newKid []byte) bool {
		fmt.Fprintf(stdout, "\na key holder that is already enrolled answered the pairing code:\n\n")
		fmt.Fprintf(stdout, "  transport id:  %s\n\n", peerID)
		fmt.Fprintf(stdout, "replace the existing entry for this key holder (keyslot %s, key %s) with this one (key %s)? [y/N] ",
			existing.Keyslot, shortKid(existing.Kid), shortKid(newKid))

		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintln(stdout, "\nno answer, so not replacing (use --yes to skip this question)")
			return false
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes"
	}
}

// pairingURL is what the QR code encodes, and what the Android app
// already knows how to parse: the same synctang://pair payload used for
// scanning a machine's identity, with the one-time code added.
//
// A machine that uses its own discovery server says so with
// discovery=, so the phone looks it up where it announces. Nothing is
// added for the public defaults, which keeps the link what older apps
// read. Neither the relay nor the announce endpoint is carried: only
// the machine joins a relay or posts an announcement, and a phone
// learns the relay from discovery.
func pairingURL(machineID, name, encodedSecret string, network transport.NetworkConfig) string {
	q := url.Values{}
	q.Set("machine", machineID)
	if name != "" {
		q.Set("name", name)
	}
	if network.Discovery != "" {
		q.Set("discovery", network.Discovery)
	}
	q.Set("psk", encodedSecret)
	return "synctang://pair?" + q.Encode()
}

// pairingInstructions is the text shown alongside the QR code, and the
// whole of what a serial console or a too-small terminal leaves a
// person with, so it has to be sufficient on its own.
func pairingInstructions(machineID, name, encodedSecret string, network transport.NetworkConfig, window time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Scan this with the synctang app to pair it with %s.\n\n", cmp.Or(name, "this machine"))
	b.WriteString("If the camera cannot scan it, paste this link into the app's\n")
	b.WriteString("device ID field instead (the QR code carries exactly this):\n\n")
	fmt.Fprintf(&b, "  %s\n\n", pairingURL(machineID, name, encodedSecret, network))
	fmt.Fprintf(&b, "The pairing code is good once, for the next %s.\n", window)
	b.WriteString("Anyone who can read it off this screen, or off a photograph of\n")
	b.WriteString("it, can ask to be enrolled, so you will be shown which key\n")
	b.WriteString("holder is asking before anything is written to the volume.\n")
	return b.String()
}
