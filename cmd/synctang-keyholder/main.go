// SPDX-License-Identifier: AGPL-3.0-or-later

// Command synctang-keyholder is the laptop key holder for synctang: it holds a
// long-term private scalar and answers a machine's recovery challenges
// on demand. There is no listening mode; the key holder always dials.
package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"github.com/muelli/synctang/internal/completion"
	"github.com/muelli/synctang/internal/gnuflag"
	"github.com/muelli/synctang/internal/ui"
	"github.com/muelli/synctang/internal/version"
	"github.com/muelli/synctang/internal/wrap"
	"github.com/muelli/synctang/mrcore"
	"io"
	"os"
	"strings"
	"time"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

const usage = `usage: synctang-keyholder <command> [flags]
       synctang-keyholder --version

commands:
  pair           enrol this key holder on a machine, from the link that
                 "synctang-unlocker pair" shows
  init           generate a new long-term key and transport identity
  export-pubkey  print the public key and transport id, as "enrol" wants them
  unlock         answer a machine's recovery attempt, by name or transport id
  machines       list, add or remove the names of machines you can unlock
  status         say where the key is kept and whether it can be read
  completion     print a shell completion script (bash, zsh or fish)

--color=auto|always|never works anywhere on the line (NO_COLOR is honoured).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ui.SetProgram("synctang-keyholder")
	rest, colour, err := ui.ExtractColorFlag(args)
	if err != nil {
		ui.Errorln(stderr, err.Error())
		return 2
	}
	ui.SetMode(colour)
	args = rest
	if len(args) < 1 {
		fmt.Fprint(stderr, completion.Reflow(usage, wrap.Width(stderr)))
		return 2
	}

	switch args[0] {
	case "--version", "-version":
		fmt.Fprint(stdout, version.Describe("synctang-keyholder"))
		return 0
	case "pair":
		return runPair(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "export-pubkey":
		return runExportPubkey(args[1:], stdout, stderr)
	case "unlock":
		return runUnlock(args[1:], stdin, stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "machines":
		return runMachines(args[1:], stdout, stderr)
	case "completion":
		return completion.Run("synctang-keyholder", usage, func(command string) string {
			if command == "completion" {
				return ""
			}
			var out bytes.Buffer
			run([]string{command, "-h"}, strings.NewReader(""), &out, &out)
			return out.String()
		}, nil, args[1:], stdout, stderr)
	default:
		ui.Errorf(stderr, "unknown command %q%s\n%s", args[0], completion.DidYouMean(args[0], usage), completion.Reflow(usage, wrap.Width(stderr)))
		return 2
	}
}

func keyFileFlag(fs *flag.FlagSet) *string {
	def, err := defaultKeyFile()
	if err != nil {
		// A missing config directory is unusual enough that failing the
		// flag default is fine; the user can still pass --key-file
		// explicitly.
		def = ""
	}
	return fs.String("key-file", def, "path to the long-term private key")
}

func transportKeyFileFlag(fs *flag.FlagSet) *string {
	def, err := defaultTransportKeyFile()
	if err != nil {
		def = ""
	}
	return fs.String("transport-key-file", def, "path to this key holder's persistent transport identity")
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	keyFile := keyFileFlag(fs)
	transportKeyFile := transportKeyFileFlag(fs)
	force := fs.Bool("force", false, "overwrite an existing key")
	storeSpec := fs.String("store", "auto", "where to keep the key: "+storeNames+" (auto is the desktop keyring when there is one, else a plain file)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *keyFile == "" {
		ui.Errorln(stderr, "--key-file is required (could not determine a default)")
		return 2
	}
	if *transportKeyFile == "" {
		ui.Errorln(stderr, "--transport-key-file is required (could not determine a default)")
		return 2
	}

	made, code := makeKey(*keyFile, *transportKeyFile, *storeSpec, *force, stderr)
	if made == nil {
		return code
	}
	st, note, pubHex, transportID := made.store, made.note, made.pubHex, made.transportID

	look := ui.For(stdout)
	fmt.Fprintf(stdout, "%s\n%s\n", look.Line(look.OK(""), look.Emoji("🔑", "")+"generated a new key, held by "+look.Bold(st.Name())),
		look.Dim(wrap.Paragraph(st.Describe(), "  ", wrap.Width(stdout))))
	if note != "" {
		ui.Notef(stderr, "%s\n", note)
	}
	fmt.Fprintf(stdout, "transport identity at %s\n\n", *transportKeyFile)

	// What happens next is enrolling this key on a machine, so say so, with the values that
	// command needs (export-pubkey prints the same two lines again).
	fmt.Fprintf(stdout, "%s %s\n%s %s\n\n", look.Bold("S:"), pubHex, look.Bold("transport-id:"), transportID)
	fmt.Fprintf(stdout, "To let a machine be unlocked with this key, the easy way is to pair with it:\n"+
		"  1. on the machine, as root:  %s\n"+
		"  2. here:                     %s\n\n",
		look.Cyan("synctang-unlocker pair --device /dev/<device>"), look.Cyan("synctang-keyholder pair '<link>'"))
	fmt.Fprintf(stdout, "Or enrol it by hand: run this on the machine, as root\n"+
		"(synctang-keyholder export-pubkey prints the two lines above again):\n\n%s\n", look.Cyan(fmt.Sprintf(
		"  synctang-unlocker enrol --device /dev/<device> --name <a name for this laptop> \\\n"+
			"    --pubkey %s \\\n"+
			"    --recipient-transport-id %s \\\n"+
			"    --existing-passphrase-file <(printf %%s \"$PASS\")", pubHex, transportID)))
	return 0
}

// madeKey is what makeKey made: where the key is kept and the two values a machine needs to enrol it.
type madeKey struct {
	store       keyStore
	note        string
	pubHex      string
	transportID string
}

// makeKey generates a long-term key into the store spec names, and the transport identity beside it
// if there is none. It reports what went wrong to stderr and returns the exit code to use.
func makeKey(keyFile, transportKeyFile, storeSpec string, force bool, stderr io.Writer) (*madeKey, int) {
	st, note, err := chooseStore(storeSpec, keyFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		if strings.Contains(err.Error(), "unknown store") {
			return nil, 2
		}
		return nil, 1
	}

	// A key that any store already holds for this key file is not replaced without --force, and
	// when it is, the old copy goes: a replaced key left behind in another store is a key nobody
	// remembers.
	var old keyStore
	if existing, err := openStore(keyFile); err == nil {
		if ok, err := existing.Exists(); err != nil && !force {
			ui.Errorf(stderr, "cannot tell whether a key is already held by %s (%v); use --force to make a new one anyway\n", existing.Name(), err)
			return nil, 1
		} else if err != nil {
			ui.Errorf(stderr, "could not check whether %s still holds the old key (%v): if it does, remove it yourself once it is reachable, because a replaced key left behind is one nobody remembers\n", existing.Name(), err)
		} else if ok {
			if !force {
				ui.Errorf(stderr, "a key is already held by %s, use --force to replace it\n", existing.Name())
				return nil, 1
			}
			old = existing
		}
	} else if !errors.Is(err, errNoKey) && !force {
		ui.Errorf(stderr, "%v; use --force to make a new key anyway\n", err)
		return nil, 1
	}

	s, err := mrcore.P256().RandomScalar()
	if err != nil {
		ui.Errorf(stderr, "generating key: %v\n", err)
		return nil, 1
	}
	defer mrcore.Wipe(s)
	if err := st.Save(s, force); err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return nil, 1
	}
	if err := writeStoreRecord(keyFile, st.Name()); err != nil {
		ui.Errorf(stderr, "the key was saved, but its record could not be written: %v\n", err)
		return nil, 1
	}
	if old != nil && old.Name() != st.Name() {
		if err := old.Delete(); err != nil {
			ui.Errorf(stderr, "the old key could not be removed from %s: %v\n", old.Name(), err)
		}
	}

	// The transport identity is created alongside the MR-1 key: both
	// together are "this key holder's identity". Unlike the scalar, it is
	// always loaded rather than regenerated when --force is given: a fresh
	// certificate here would change this key holder's Device ID, silently
	// breaking every AuthorizedPeers list it was already enrolled into.
	cert, err := transport.LoadOrCreateCert(transportKeyFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return nil, 1
	}
	pub, err := mrcore.P256().ScalarBaseMult(s)
	if err != nil {
		ui.Errorf(stderr, "computing public key: %v\n", err)
		return nil, 1
	}
	pubHex := hex.EncodeToString(pub)
	transportID := protocol.NewDeviceID(cert.Certificate[0]).String()

	return &madeKey{store: st, note: note, pubHex: pubHex, transportID: transportID}, 0
}

func runExportPubkey(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export-pubkey", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	keyFile := keyFileFlag(fs)
	transportKeyFile := transportKeyFileFlag(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *keyFile == "" {
		ui.Errorln(stderr, "--key-file is required (could not determine a default)")
		return 2
	}
	if *transportKeyFile == "" {
		ui.Errorln(stderr, "--transport-key-file is required (could not determine a default)")
		return 2
	}

	pubHex, err := publicKeyHex(*keyFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}

	cert, err := transport.LoadOrCreateCert(*transportKeyFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "S: %s\n", pubHex)
	fmt.Fprintf(stdout, "transport-id: %s\n", protocol.NewDeviceID(cert.Certificate[0]))
	return 0
}

func runUnlock(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unlock", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	keyFile := keyFileFlag(fs)
	transportKeyFile := transportKeyFileFlag(fs)
	directAddr := fs.String("direct-addr", "", "dial this address directly, bypassing relay and discovery (testing only)")
	yes := fs.Bool("yes", false, "skip the approval prompt")
	timeout := fs.Duration("timeout", defaultUnlockTimeout, "give up dialling the machine after this long")
	discovery := fs.String("discovery", "", "discovery server URL, instead of the public one; the machine must announce to the same one")
	discoveryAnnounce := fs.String("discovery-announce", "", "discovery announce URL, if it differs from --discovery")
	machinesFile := machinesFileFlag(fs)
	configFile := fs.String("config", "", "file with discovery and discovery-announce settings (default: synctang-keyholder/config in the user config directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *machinesFile == "" {
		ui.Errorln(stderr, "--machines-file is required (could not determine a default)")
		return 2
	}
	which := ""
	switch fs.NArg() {
	case 0:
		// With one machine known there is nothing to choose.
		known, err := loadMachines(*machinesFile)
		switch {
		case err != nil:
			ui.Errorf(stderr, "%v\n", err)
			return 2
		case len(known) == 0:
			ui.Errorln(stderr, "no machine is named yet, so there is nothing to unlock without naming it: pair with one (synctang-keyholder pair '<link>'), "+
				"or give the machine's transport id")
			return 2
		case len(known) > 1:
			names := make([]string, len(known))
			for i, m := range known {
				names[i] = m.Name
			}
			ui.Errorf(stderr, "which machine? %s: synctang-keyholder unlock <name>\n", strings.Join(names, ", "))
			return 2
		}
		which = known[0].Name
	case 1:
		which = fs.Arg(0)
	default:
		ui.Errorln(stderr, "unlock takes one machine, by name or transport id")
		return 2
	}
	target, err := resolveMachine(*machinesFile, which)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 2
	}
	machineID := target.ID
	if *discovery == "" {
		*discovery = target.Discovery
	}

	configPath, explicitConfig := *configFile, *configFile != ""
	if !explicitConfig {
		// No default is not fatal: the file is optional.
		configPath, _ = defaultConfigFile()
	}
	network, err := loadNetworkConfig(configPath, explicitConfig, *discovery, *discoveryAnnounce)
	if err != nil {
		ui.Errorf(stderr, "network settings: %v\n", err)
		return 2
	}
	if s := network.Summary(); s != "" {
		ui.Infof(stderr, "%s\n", s)
	}

	if *keyFile == "" {
		ui.Errorln(stderr, "--key-file is required (could not determine a default)")
		return 2
	}
	if *transportKeyFile == "" {
		ui.Errorln(stderr, "--transport-key-file is required (could not determine a default)")
		return 2
	}

	s, err := loadPrivateKey(*keyFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}
	defer mrcore.Wipe(s)

	cert, err := transport.LoadOrCreateCert(*transportKeyFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Retried, not just attempted once: Multi races LocalDiscovery
	// against SyncthingRelay independently on each side, so the
	// machine and this key holder can each pick a different winning
	// transport for what was meant to be the same attempt (one
	// genuinely authenticated connection with nobody on the other end
	// reading or writing it), surfacing as a read failure partway
	// through the exchange rather than a dial failure. Found running
	// this against a real deployment. A fresh attempt is a fresh,
	// independent race on both sides, so retrying converges rather
	// than repeating the same mismatch.
	status := ui.NewStatus(stderr)
	defer status.Stop()
	started := time.Now()
	what := cmp.Or(target.Name, target.ID)
	status.Set(func() string { return fmt.Sprintf("looking for %s (%s)", what, elapsed(started)) })
	var lastErr error
	for {
		lastErr = runUnlockOnce(ctx, s, cert, machineID, target.Name, *directAddr, network, *yes, stdin, stdout, status.Stop)
		if lastErr == nil {
			return 0
		}
		select {
		case <-ctx.Done():
			status.Stop()
			ui.Errorf(stderr, "%v (last attempt: %v)\n", ctx.Err(), lastErr)
			var dial *dialError
			if *directAddr == "" && errors.As(lastErr, &dial) {
				if hint := dialFailureHint(machineID, network.Discovery, time.Now); hint != "" {
					ui.Notef(stderr, "%s\n", hint)
				}
			}
			return 1
		case <-time.After(unlockRetryInterval):
		}
	}
}

// dialFailureHint asks the discovery server once what it knows of the machine, after a dial
// gave up, and returns what to tell the person (empty when discovery says nothing useful,
// such as when it could not be reached or asked to be left alone). The dial's own time is
// spent, so the lookup has a short limit of its own.
func dialFailureHint(machineID, discovery string, now func() time.Time) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return transport.DialFailureHint(transport.CheckMachine(ctx, machineID, discovery, now))
}

// unlockRetryInterval is how long runUnlock waits between attempts
// after one fails. Matches the machine agent's own agentRetryInterval
// in spirit: fast enough not to waste the --timeout budget, slow
// enough not to hammer the relay pool.
const unlockRetryInterval = 2 * time.Second

// defaultUnlockTimeout bounds how long "unlock" will keep retrying the
// dial (mrcore/transport.SyncthingRelay.Dial now retries the relay path
// indefinitely on its own, until told to stop): a machine that is
// genuinely not there, or an unenrolled or mistyped transport ID, must
// eventually give up and say so rather than hang forever. Two minutes
// covers the relay pool's own connection churn (see the transport
// package's dialRelay comment) with room to spare.
const defaultUnlockTimeout = 2 * time.Minute
