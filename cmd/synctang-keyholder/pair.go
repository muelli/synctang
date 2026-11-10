// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/muelli/synctang/internal/gnuflag"
	"github.com/muelli/synctang/internal/wrap"
	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// pairLink is what "synctang-unlocker pair" shows as a link and a QR code: the machine to dial, its
// name, the discovery server it announces to if that is not the public one, and the one-time code.
type pairLink struct {
	Machine   string
	Name      string
	Discovery string
	Code      string
}

func parsePairingLink(text string) (pairLink, error) {
	bad := func(why string) (pairLink, error) {
		return pairLink{}, fmt.Errorf("that is not a pairing link (%s): it is the synctang://pair?... link that \"synctang-unlocker pair\" prints under its QR code", why)
	}
	u, err := url.Parse(text)
	if err != nil || u.Scheme != "synctang" || u.Host != "pair" {
		return bad("it does not start with synctang://pair")
	}
	q := u.Query()
	l := pairLink{Machine: q.Get("machine"), Name: q.Get("name"), Discovery: q.Get("discovery"), Code: q.Get("psk")}
	if l.Machine == "" || l.Code == "" {
		return bad("it has no machine or no code")
	}
	id, err := protocol.DeviceIDFromString(l.Machine)
	if err != nil {
		return bad("the machine in it is not a transport id")
	}
	l.Machine = id.String()
	secret, err := mrcore.DecodePairingSecret(l.Code)
	if err != nil {
		return bad("the code in it is not valid")
	}
	mrcore.Wipe(secret)
	return l, nil
}

// pairFinal marks a failure after which another attempt cannot help: the machine has the proof of the
// code (a second dial would spend the one-time code again) or has given its answer.
type pairFinal struct{ error }

func (e *pairFinal) Unwrap() error { return e.error }

// pairOnce dials the machine, answers its challenge with a proof of the code, prints the short code the
// machine will show its operator (via announce) and waits for the machine's verdict.
func pairOnce(ctx context.Context, wait time.Duration, cert tls.Certificate, link pairLink, pub []byte, name, directAddr string,
	network transport.NetworkConfig, announce func(sas string)) error {
	secret, err := mrcore.DecodePairingSecret(link.Code)
	if err != nil {
		return &pairFinal{err}
	}
	defer mrcore.Wipe(secret)
	selfID := protocol.NewDeviceID(cert.Certificate[0]).String()

	tr := newDialTransport(cert, directAddr, network)
	conn, err := tr.Dial(ctx, transport.DialOptions{PeerID: link.Machine, DirectAddr: directAddr})
	if err != nil {
		return &dialError{fmt.Errorf("dialing %s: %w", link.Machine, err)}
	}
	defer conn.Close()
	// Reads have no deadline: closing the connection is what interrupts one.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer func() { stop() }()

	var challenge mrcore.PairChallenge
	if err := mrcore.ReadMessage(conn, &challenge); err != nil {
		return fmt.Errorf("reading the pairing challenge: %w", err)
	}
	req := mrcore.PairRequest{PubKey: pub, Name: name, Proof: mrcore.PairProof(secret, link.Machine, selfID, pub, challenge.Nonce)}
	if err := mrcore.WriteMessage(conn, req); err != nil {
		return fmt.Errorf("sending the pairing request: %w", err)
	}
	stop()

	// Both sides can work out the short code from what they hold; the machine shows it to its operator
	// with the question whether to enrol this key holder.
	announce(mrcore.PairSAS(link.Machine, selfID, pub, challenge.Nonce))

	waitCtx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	stop = context.AfterFunc(waitCtx, func() { conn.Close() })
	var result mrcore.PairResult
	if err := mrcore.ReadMessage(conn, &result); err != nil {
		if waitCtx.Err() != nil {
			return &pairFinal{fmt.Errorf("the machine did not answer within %s", wait)}
		}
		return &pairFinal{fmt.Errorf("the machine had the pairing request but its answer did not arrive: %w", err)}
	}
	if !result.OK {
		return &pairFinal{pairRefusal(result.Error)}
	}
	return nil
}

// pairRefusal says in words what the machine's reason means for the person at the laptop.
func pairRefusal(reason string) error {
	switch reason {
	case mrcore.PairRefusedCodeMismatch:
		return errors.New("the machine did not accept the pairing code: it is wrong, was already used, or has expired " +
			"(a code is good for one pairing and five minutes). Run \"synctang-unlocker pair\" on the machine again for a new link")
	case mrcore.PairRefusedDeclined:
		return errors.New("the person at the machine declined the pairing. If the code they were shown was not the one above, " +
			"somebody else used the pairing link first: run \"synctang-unlocker pair\" again")
	case "":
		return errors.New("the machine refused the pairing")
	}
	return fmt.Errorf("the machine refused the pairing: %s", reason)
}

const (
	defaultPairTimeout = 2 * time.Minute
	// defaultPairWait is how long to wait for the person at the machine, matching the machine's own
	// five-minute window.
	defaultPairWait = 5 * time.Minute
)

func runPair(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	keyFile := keyFileFlag(fs)
	transportKeyFile := transportKeyFileFlag(fs)
	machinesFile := machinesFileFlag(fs)
	storeSpec := fs.String("store", "auto", "if there is no key yet, where to keep the one made: "+storeNames)
	name := fs.String("name", "", "the name this key holder gives itself, shown on the machine (default: this computer's host name)")
	as := fs.String("as", "", "the name to unlock this machine by (default: the name in the link)")
	directAddr := fs.String("direct-addr", "", "dial this address directly, bypassing relay and discovery (testing only)")
	timeout := fs.Duration("timeout", defaultPairTimeout, "give up reaching the machine after this long")
	wait := fs.Duration("wait", defaultPairWait, "how long to wait for the person at the machine to confirm")
	discovery := fs.String("discovery", "", "discovery server URL, instead of the one in the link or the public one")
	configFile := fs.String("config", "", "file with discovery settings (default: synctang-keyholder/config in the user config directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "synctang-keyholder: pair wants one argument, the pairing link that \"synctang-unlocker pair\" prints under its QR code (in quotes)")
		return 2
	}
	link, err := parsePairingLink(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "synctang-keyholder: %v\n", err)
		return 2
	}
	if *keyFile == "" || *transportKeyFile == "" || *machinesFile == "" {
		fmt.Fprintln(stderr, "synctang-keyholder: --key-file, --transport-key-file and --machines-file are required (no default could be determined)")
		return 2
	}
	alias := cmp.Or(*as, link.Name)
	if alias != "" {
		if err := validMachineName(alias); err != nil {
			fmt.Fprintf(stderr, "synctang-keyholder: %v (name it yourself with --as)\n", err)
			return 2
		}
	}

	configPath, explicitConfig := *configFile, *configFile != ""
	if !explicitConfig {
		configPath, _ = defaultConfigFile()
	}
	chosenDiscovery := cmp.Or(*discovery, link.Discovery)
	network, err := loadNetworkConfig(configPath, explicitConfig, chosenDiscovery, "")
	if err != nil {
		fmt.Fprintf(stderr, "synctang-keyholder: network settings: %v\n", err)
		return 2
	}

	// A key holder that has no key yet makes one: pairing is how a laptop is meant to be enrolled, so
	// it should not need a separate step first.
	if _, err := openStore(*keyFile); errors.Is(err, errNoKey) {
		made, code := makeKey(*keyFile, *transportKeyFile, *storeSpec, false, stderr)
		if made == nil {
			return code
		}
		fmt.Fprintf(stdout, "made a new key, held by %s\n%s\n\n", made.store.Name(), wrap.Paragraph(made.store.Describe(), "  ", textWidth))
		if made.note != "" {
			fmt.Fprintf(stderr, "synctang-keyholder: %s\n", made.note)
		}
	} else if err != nil {
		fmt.Fprintf(stderr, "synctang-keyholder: %v\n", err)
		return 1
	}
	pubHex, err := publicKeyHex(*keyFile)
	if err != nil {
		fmt.Fprintf(stderr, "synctang-keyholder: %v\n", err)
		return 1
	}
	pub, err := hex.DecodeString(pubHex)
	if err != nil {
		fmt.Fprintf(stderr, "synctang-keyholder: %v\n", err)
		return 1
	}
	cert, err := transport.LoadOrCreateCert(*transportKeyFile)
	if err != nil {
		fmt.Fprintf(stderr, "synctang-keyholder: %v\n", err)
		return 1
	}
	selfName := *name
	if selfName == "" {
		selfName, _ = os.Hostname()
	}

	announce := func(sas string) {
		fmt.Fprintf(stdout, "\nAsking %s to enrol this key holder. The machine shows its operator a code,\n"+
			"and it must be the same as this one:\n\n    %s\n\n"+
			"If it is different, answer n on the machine: somebody else used the pairing link first.\n"+
			"Waiting for the person at the machine to confirm (up to %s)...\n", cmp.Or(link.Name, link.Machine), sas, wait.Round(time.Second))
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	for {
		err = pairOnce(ctx, *wait, cert, link, pub, selfName, *directAddr, network, announce)
		if err == nil {
			break
		}
		var final *pairFinal
		if errors.As(err, &final) {
			fmt.Fprintf(stderr, "synctang-keyholder: %v\n", final.error)
			return 1
		}
		select {
		case <-ctx.Done():
			fmt.Fprintf(stderr, "synctang-keyholder: could not reach the machine to pair: %v (last attempt: %v)\n", ctx.Err(), err)
			return 1
		case <-time.After(unlockRetryInterval):
		}
	}

	fmt.Fprintf(stdout, "\npaired: %s has enrolled this key holder.\n", cmp.Or(link.Name, link.Machine))
	if alias == "" {
		fmt.Fprintf(stdout, "Unlock it with: synctang-keyholder unlock %s\n"+
			"(give it a name with: synctang-keyholder machines add <name> %s)\n", link.Machine, link.Machine)
		return 0
	}
	previous, replaced, err := putMachine(*machinesFile, machine{Name: alias, ID: link.Machine, Discovery: chosenDiscovery}, true)
	if err != nil {
		fmt.Fprintf(stdout, "Unlock it with: synctang-keyholder unlock %s\n", link.Machine)
		fmt.Fprintf(stderr, "synctang-keyholder: paired, but the name %q could not be saved: %v\n", alias, err)
		return 1
	}
	if replaced && previous.ID != link.Machine {
		fmt.Fprintf(stdout, "(the name %s used to mean %s; it now means this machine)\n", alias, previous.ID)
	}
	fmt.Fprintf(stdout, "Unlock it with: synctang-keyholder unlock %s\n", alias)
	return 0
}
