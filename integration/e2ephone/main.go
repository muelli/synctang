// SPDX-License-Identifier: AGPL-3.0-or-later

// Command e2ephone is a key holder that behaves as the Android app does, for the
// end-to-end test (scripts/e2e-vm.sh): it pairs from a pairing link and answers a
// machine's request, through the same Go library the app is built on (package
// mobile), with the key in a file under a directory the test owns instead of in the
// Android Keystore. So it exercises the phone's Go half against a real machine, not
// the hardware-bound key, the fingerprint or the Kotlin screens.
//
// A test tool and nothing else: its key is a plain file.
//
//	e2ephone init   DIR             make a seed and a key; print the Device ID
//	e2ephone pair   DIR LINK        pair from the link "synctang-unlocker pair" shows
//	e2ephone unlock DIR MACHINE-ID [DISCOVERY-URL]
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/muelli/synctang/mobile"
)

// link is what a pairing link carries.
type link struct {
	machine, name, code, discovery string
}

func parseLink(s string) (link, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "synctang" || u.Host != "pair" {
		return link{}, fmt.Errorf("not a synctang pairing link: %q", s)
	}
	q := u.Query()
	l := link{machine: q.Get("machine"), name: q.Get("name"), code: q.Get("psk"), discovery: q.Get("discovery")}
	if l.machine == "" || l.code == "" {
		return link{}, fmt.Errorf("the pairing link has no machine or no code: %q", s)
	}
	return l, nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "e2ephone:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: e2ephone init|pair|unlock DIR ...")
	}
	cmd, dir := args[0], args[1]
	switch cmd {
	case "init":
		return initPhone(dir)
	case "pair":
		if len(args) != 3 {
			return fmt.Errorf("usage: e2ephone pair DIR LINK")
		}
		return pair(dir, args[2])
	case "unlock":
		if len(args) < 3 || len(args) > 4 {
			return fmt.Errorf("usage: e2ephone unlock DIR MACHINE-ID [DISCOVERY-URL]")
		}
		discovery := ""
		if len(args) == 4 {
			discovery = args[3]
		}
		return unlock(dir, args[2], discovery)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func initPhone(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	seed := make([]byte, 16)
	if _, err := rand.Read(seed); err != nil {
		return err
	}
	pair, err := mobile.NewKeyPair()
	if err != nil {
		return err
	}
	defer pair.Wipe()
	if err := os.WriteFile(filepath.Join(dir, "seed"), []byte(hex.EncodeToString(seed)), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "scalar"), []byte(hex.EncodeToString(pair.Scalar())), 0o600); err != nil {
		return err
	}
	id, err := mobile.DeviceIDForSeed(hex.EncodeToString(seed))
	if err != nil {
		return err
	}
	fmt.Println("device id:", id)
	fmt.Println("public key:", hex.EncodeToString(pair.PublicPoint()))
	return nil
}

type phone struct {
	seed   string
	scalar []byte
	public []byte
	pubHex string
}

func load(dir string) (*phone, error) {
	seed, err := os.ReadFile(filepath.Join(dir, "seed"))
	if err != nil {
		return nil, err
	}
	hexScalar, err := os.ReadFile(filepath.Join(dir, "scalar"))
	if err != nil {
		return nil, err
	}
	scalar, err := hex.DecodeString(strings.TrimSpace(string(hexScalar)))
	if err != nil {
		return nil, err
	}
	public, err := mobile.PublicPointOf(scalar)
	if err != nil {
		return nil, err
	}
	return &phone{seed: strings.TrimSpace(string(seed)), scalar: scalar, public: public, pubHex: hex.EncodeToString(public)}, nil
}

func pair(dir, linkText string) error {
	p, err := load(dir)
	if err != nil {
		return err
	}
	l, err := parseLink(linkText)
	if err != nil {
		return err
	}
	pairer := mobile.NewPairer()
	if l.discovery != "" {
		if err := pairer.SetDiscovery(l.discovery); err != nil {
			return err
		}
	}
	if err := pairer.Pair(l.machine, l.code, p.seed, p.pubHex, "e2e-phone", "", 180); err != nil {
		return fmt.Errorf("pairing: %w", err)
	}
	// What the app shows while the operator decides; kept after Pair returns, so printing it here is
	// enough for a test to compare it with the machine's.
	fmt.Println("code:", pairer.ShortCode())
	fmt.Println("paired with", l.machine)
	return nil
}

func unlock(dir, machine, discovery string) error {
	p, err := load(dir)
	if err != nil {
		return err
	}
	s := mobile.NewSession(machine, p.seed)
	if discovery != "" {
		if err := s.SetDiscovery(discovery); err != nil {
			return err
		}
	}
	s.SetTimeoutSeconds(240)
	if err := s.Connect(); err != nil {
		return fmt.Errorf("connecting: %w", err)
	}
	defer s.Close()
	if !s.HasRequest() {
		return fmt.Errorf("the machine connected and asked for nothing")
	}
	if err := s.AnswerWithScalar(p.scalar, p.public); err != nil {
		return fmt.Errorf("answering: %w", err)
	}
	fmt.Println("unlocked")
	return nil
}
