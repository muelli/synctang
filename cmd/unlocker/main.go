// SPDX-License-Identifier: AGPL-3.0-or-later

// Command unlocker is the machine side of synctang: it runs in the
// dracut initrd and, after boot, as a systemd password agent. It
// always listens for a key holder to dial in; it never dials out.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"flag"
	"fmt"
	"github.com/muelli/synctang/mrcore"
	"io"
	"log/slog"
	"math/big"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

const usage = `usage: unlocker <command> [flags]

commands:
  enrol   add a key holder's public key to a LUKS volume
  agent   run the systemd password agent, answering recovery attempts
`

// defaultMachineKeyFile is where this machine's own persistent transport
// identity lives, unless overridden. It must exist at the same path both
// when a human runs "unlocker enrol" and inside the dracut initrd the
// "agent" subcommand later runs in, or the Device ID a key holder paired
// against will not match the one the agent presents at boot; see
// dracut/90unlocker/module-setup.sh, which copies this file into the
// initrd when present.
const defaultMachineKeyFile = "/etc/unlocker/machine.pem"

// defaultAskPasswordDir is where systemd's password agent protocol leaves
// pending requests; see https://systemd.io/PASSWORD_AGENTS/.
const defaultAskPasswordDir = "/run/systemd/ask-password"

// agentRetryInterval is how long the agent waits between recovery attempts
// after one ends (successfully or not) before listening for the next one.
// Not load-bearing; anywhere from one to a few seconds is fine.
const agentRetryInterval = 2 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	switch args[0] {
	case "enrol":
		return runEnrol(args[1:], stdout, stderr)
	case "agent":
		return runAgentCLI(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unlocker: unknown command %q\n%s", args[0], usage)
		return 2
	}
}

func runEnrol(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("enrol", flag.ContinueOnError)
	fs.SetOutput(stderr)
	device := fs.String("device", "", "LUKS2 device or image to enrol")
	pubkeyHex := fs.String("pubkey", "", "recipient's public key, hex-encoded (from keyholder export-pubkey)")
	passphraseFile := fs.String("existing-passphrase-file", "", "file containing an existing valid LUKS passphrase (- for stdin)")
	name := fs.String("name", "", "this machine's name, shown to the key holder")
	transportID := fs.String("transport-id", "", "this machine's transport identity")
	recipientTransportID := fs.String("recipient-transport-id", "", "the recipient's own transport identity (from keyholder export-pubkey), if known")
	machineKeyFile := fs.String("machine-key-file", defaultMachineKeyFile, "path to this machine's persistent transport identity")
	remove := fs.String("remove", "", "revoke the enrolled recipient with this transport id instead of adding one")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *remove != "" {
		if *device == "" || *passphraseFile == "" {
			fmt.Fprintln(stderr, "unlocker: --device and --existing-passphrase-file are required with --remove")
			return 2
		}
		passphrase, err := readPassphrase(*passphraseFile)
		if err != nil {
			fmt.Fprintf(stderr, "unlocker: %v\n", err)
			return 1
		}
		defer mrcore.Wipe(passphrase)

		if err := removeRecipient(*device, passphrase, *remove); err != nil {
			fmt.Fprintf(stderr, "unlocker: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "revoked recipient %s on %s\n", *remove, *device)
		return 0
	}

	if *device == "" || *pubkeyHex == "" || *passphraseFile == "" {
		fmt.Fprintln(stderr, "unlocker: --device, --pubkey and --existing-passphrase-file are all required")
		return 2
	}

	pubkey, err := hex.DecodeString(*pubkeyHex)
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: --pubkey: %v\n", err)
		return 2
	}

	passphrase, err := readPassphrase(*passphraseFile)
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: %v\n", err)
		return 1
	}
	defer mrcore.Wipe(passphrase)

	if err := enrolRecipient(*device, passphrase, pubkey, *name, *transportID, *recipientTransportID); err != nil {
		fmt.Fprintf(stderr, "unlocker: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "enrolled a new recipient on %s\n", *device)

	// The human doing this by hand needs this machine's own transport id
	// to hand to a key holder, in case it is ever needed (for example to
	// fill in --recipient-transport-id on a later enrolment, mirrored the
	// other way).
	// Whether the identity already existed has to be established
	// before LoadOrCreateCert, which does not say which it did.
	//
	// --device can be a disk image belonging to some other machine, a
	// documented use, and then this flag's default is wrong: it points
	// at this host's identity, which for an image is nobody's. enrol
	// silently generates a new one and prints it as "this machine's
	// transport id", so a key holder paired against that id waits
	// forever for a machine announcing a different one. Hit exactly
	// that way enrolling a phone onto a test VM's image.
	_, statErr := os.Stat(*machineKeyFile)
	machineIdentityIsNew := os.IsNotExist(statErr)

	cert, err := transport.LoadOrCreateCert(*machineKeyFile)
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: loading this machine's transport identity: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "this machine's transport id: %s\n", protocol.NewDeviceID(cert.Certificate[0]))
	if shouldWarnAboutNewIdentity(machineIdentityIsNew, *machineKeyFile) {
		fmt.Fprintf(stderr,
			"unlocker: created a new transport identity at %s, so the id above is this host's.\n"+
				"unlocker: if --device is a disk image for another machine, pass --machine-key-file pointing\n"+
				"unlocker: into that image and re-run, or the key holder will pair against an id no machine\n"+
				"unlocker: ever announces.\n",
			*machineKeyFile)
	}
	return 0
}

// shouldWarnAboutNewIdentity reports whether enrol has just invented a
// machine identity in a way the caller probably did not intend.
//
// Creating one at a path the caller named is exactly what they asked
// for, and the id printed then belongs to the enrolled device.
// Creating one at the default path while enrolling somebody else's
// disk image is the mistake worth catching: the id printed is this
// host's, and a key holder paired against it waits for a machine that
// announces a different one. enrol cannot tell the two situations
// apart from --device alone, so it goes by whether the caller said
// where the identity lives.
func shouldWarnAboutNewIdentity(isNew bool, keyFile string) bool {
	return isNew && keyFile == defaultMachineKeyFile
}

// generateConfirmCode picks a random 6-digit code, printed on this
// machine's console and relayed back by the key holder before recovery
// proceeds, so that whoever answers has proven they can see this
// console, not merely reach it over the network.
func generateConfirmCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("generating confirm code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// runAgentCLI is the "agent" subcommand: it runs the systemd password
// agent, listening for a key holder to connect and answer one recovery
// attempt at a time, until ctx (SIGTERM, which
// dracut/90unlocker/unlocker-stop.sh sends) is cancelled.
func runAgentCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	device := fs.String("device", "", "LUKS2 device to unlock (auto-discovered from /proc/partitions if omitted)")
	name := fs.String("name", "", "this machine's name, shown to the key holder")
	confirmCodeFlag := fs.Bool("confirm-code", false, "require the key holder to read back a code shown on this console")
	machineKeyFile := fs.String("machine-key-file", defaultMachineKeyFile, "path to this machine's persistent transport identity")
	askPasswordDir := fs.String("ask-password-dir", defaultAskPasswordDir, "systemd ask-password directory")
	directAddr := fs.String("direct-addr", "", "listen on this address directly, bypassing relay and discovery (testing only)")
	logLevelFlag := fs.String("log-level", string(logLevelInfo), "logging level: trace, debug, info, warn, error")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	logLevel, err := parseLogLevel(*logLevelFlag)
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: %v\n", err)
		return 2
	}
	logger := newLogger(logLevel)
	slog.SetDefault(logger)

	cert, err := transport.LoadOrCreateCert(*machineKeyFile)
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: %v\n", err)
		return 1
	}

	var confirmCode string
	if *confirmCodeFlag {
		confirmCode, err = generateConfirmCode()
		if err != nil {
			fmt.Fprintf(stderr, "unlocker: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "confirm code: %s\n", confirmCode)
	}

	fmt.Fprintf(stdout, "unlocker: waiting for a key holder, transport id %s\n", protocol.NewDeviceID(cert.Certificate[0]))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()

	tr := newAgentTransport(cert, logger, *directAddr)
	for ctx.Err() == nil {
		// Checked on every pass, not once at startup: this hook runs
		// from dracut's initqueue "settled", which fires once udev has
		// settled, not once the network is up, so a one-shot check
		// here would see "no resolver yet" and never look again. Every
		// discovery lookup and relay connection needs DNS, so without
		// this the agent can sit here indefinitely with a live network
		// it cannot actually use for anything name-based.
		ensureResolver()

		activeDevice := *device
		if activeDevice == "" {
			// The real dracut deployment has no --device to pass: the
			// hook script that starts this (unlocker-start.sh) has no
			// way to know the device name either. Re-discover every
			// attempt, not just once at startup, in case the volume
			// was not yet visible in /proc/partitions the first time
			// around (this hook runs on udev "settled", not after the
			// network or every device enumeration is guaranteed done).
			found, err := findMr1Device()
			if err != nil {
				logger.Log(ctx, levelTrace, "device discovery failed, will retry", "error", err)
				select {
				case <-ctx.Done():
				case <-time.After(agentRetryInterval):
				}
				continue
			}
			activeDevice = found
		}

		logger.Log(ctx, levelTrace, "starting a recovery attempt", "device", activeDevice)
		err := runAgentOnce(ctx, activeDevice, cert, *name, confirmCode, *askPasswordDir,
			transport.ListenOptions{DirectAddr: *directAddr}, tr)
		switch {
		case err == nil:
			fmt.Fprintln(stdout, "unlocker: unlocked")
			logger.Info("recovery attempt succeeded")
		case ctx.Err() != nil:
			// Shutting down; the error is just ctx being cancelled mid-attempt.
		default:
			fmt.Fprintf(stderr, "unlocker: %v\n", err)
			logger.Error("recovery attempt failed", "error", err)
		}

		select {
		case <-ctx.Done():
		case <-time.After(agentRetryInterval):
		}
	}
	return 0
}

// newAgentTransport builds the Transport the agent listens through. An
// explicit directAddr bypasses discovery entirely (offline tests, and
// the escape hatch for a network that blocks both the relay and local
// multicast); otherwise LocalDiscovery and SyncthingRelay are raced
// together (transport.Multi), so a key holder on the same LAN can
// unlock this machine even with no Internet route at all, without
// either side needing to know in advance which situation it is in.
// LocalDiscovery needs no DNS resolver, unlike SyncthingRelay, so it
// keeps working even when ensureResolver above never finds one.
func newAgentTransport(cert tls.Certificate, logger *slog.Logger, directAddr string) transport.Transport {
	if directAddr != "" {
		return &transport.SyncthingRelay{Cert: cert, Logger: logger}
	}
	return &transport.Multi{Transports: []transport.Transport{
		&transport.LocalDiscovery{Cert: cert, Logger: logger},
		&transport.SyncthingRelay{Cert: cert, Logger: logger},
	}}
}

// readPassphrase reads an existing LUKS passphrase from path (stdin if
// "-"), stripping exactly one trailing newline (and a preceding \r).
// A human typing a passphrase at cryptsetup's own interactive prompt
// never has it include the newline the terminal driver consumed; a
// passphrase saved to a file with a text editor, or piped with a
// shell here-string, almost always does. Without this, --existing-
// passphrase-file silently fails to match a passphrase that was in
// fact typed correctly, for a reason invisible in the file's content.
func readPassphrase(path string) ([]byte, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	data = bytes.TrimSuffix(data, []byte("\r"))
	return data, nil
}
