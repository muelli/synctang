// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/muelli/synctang/mrcore"
)

// credsName is the name the credential is sealed under; decrypting needs the same one.
const credsName = "synctang-keyholder"

// credsRun runs systemd-creds with stdin and returns its stdout; a variable so that tests can
// stand in for the tool. The scalar goes to it on stdin and never in an argument or a file.
var credsRun = func(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("systemd-creds", args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return nil, fmt.Errorf("systemd-creds: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("systemd-creds: %w", err)
	}
	return stdout.Bytes(), nil
}

// credsAvailable is the probe used to decide whether --store systemd-creds can work here.
var credsAvailable = systemdCredsAvailable

// systemdCredsAvailable seals and unseals a throwaway value, which is the only way to know that
// user-scope credentials work for this user on this machine (system scope is refused to ordinary
// users, and user scope needs systemd 256 or later).
func systemdCredsAvailable(tpm2 bool) error {
	if tpm2 {
		if err := tpmPresent(); err != nil {
			return fmt.Errorf("there is no usable TPM here, and systemd-creds would quietly seal to the machine key alone: %w", err)
		}
	}
	probe := []byte("probe")
	sealed, err := credsRun(probe, credsArgs("encrypt", tpm2)...)
	if err != nil {
		return err
	}
	back, err := credsRun(sealed, credsArgs("decrypt", false)...)
	if err != nil {
		return err
	}
	if !bytes.Equal(back, probe) {
		return errors.New("systemd-creds did not return what it was given")
	}
	return nil
}

// tpmPresent says whether a TPM is fully usable. systemd-creds does not: asked for a TPM key
// where there is none, it falls back to the key without it and says nothing, which is why this
// is asked first. A variable so that tests can say whether there is one.
var tpmPresent = func() error {
	if out, err := exec.Command("systemd-analyze", "has-tpm2", "-q").CombinedOutput(); err != nil {
		return fmt.Errorf("systemd-analyze has-tpm2: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func credsArgs(verb string, tpm2 bool) []string {
	args := []string{verb, "--user", "--name=" + credsName}
	if tpm2 && verb == "encrypt" {
		// Not "tpm2": in user scope systemd refuses it. host+tpm2 is the TPM-bound key it allows.
		args = append(args, "--with-key=host+tpm2")
	}
	return append(args, "-", "-")
}

// credStore keeps the scalar sealed by systemd-creds, in the user's own scope, in a file beside the
// key file. Nothing is linked: the program runs the tool, as it runs cryptsetup.
type credStore struct {
	blob string
	tpm2 bool
}

func newCredStore(blob string, tpm2 bool) *credStore { return &credStore{blob: blob, tpm2: tpm2} }

func (c *credStore) Name() string {
	if c.tpm2 {
		return "systemd-creds:tpm2"
	}
	return "systemd-creds"
}

func (c *credStore) Location() string { return "sealed credential " + c.blob }

func (c *credStore) Describe() string {
	d := "a systemd credential (" + c.blob + "), sealed by systemd-creds in your own scope with a key systemd keeps for your account on this machine; " +
		"user-scope credentials need systemd 256 or later. A copy of the blob alone is useless anywhere else. " +
		"It does not need a desktop session or a D-Bus session, so it works over ssh. But root can unseal it, and software running as you " +
		"can run systemd-creds itself, and the key that unseals it is on the same disk: an image of the whole disk of a laptop whose disk is not encrypted gives both"
	if c.tpm2 {
		d += ". It is also sealed to this machine's TPM, so a copy of the disk is useless without that TPM; " +
			"the price is that the key cannot be moved to another machine and may be lost if the TPM or its measured state changes, " +
			"so keep a backup of the key some other way"
	}
	return d
}

// unusable is an error from the tool that is not "there is no key": it did not run, or would not
// unseal. Said so, with what the store needs, because "no key" would send a person to make a new
// one while theirs is simply out of reach.
func credsUnusable(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "Name in credential") || strings.Contains(msg, "Bad credential format") {
		return fmt.Errorf("the file is not a credential this store sealed for this key (a wrong file, or one sealed under another name): %w", err)
	}
	return fmt.Errorf("systemd-creds could not be used (user-scope credentials need systemd 256 or later and a login session of your own): %w", err)
}

func (c *credStore) Exists() (bool, error) {
	_, err := os.Stat(c.blob)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	}
	return false, err
}

func (c *credStore) Load() ([]byte, error) {
	sealed, err := os.ReadFile(c.blob)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w at %s", errNoKey, c.blob)
	}
	if err != nil {
		return nil, err
	}
	plain, err := credsRun(sealed, credsArgs("decrypt", false)...)
	if err != nil {
		return nil, credsUnusable(err)
	}
	defer mrcore.Wipe(plain)
	return scalarFromHex(plain, "the credential "+c.blob)
}

func (c *credStore) Save(scalar []byte, overwrite bool) error {
	if !overwrite {
		if ok, err := c.Exists(); err != nil {
			return err
		} else if ok {
			return fmt.Errorf("%s already holds a key, use --force to overwrite", c.blob)
		}
	}
	plain := hexBytes(scalar)
	defer mrcore.Wipe(plain)
	sealed, err := credsRun(plain, credsArgs("encrypt", c.tpm2)...)
	if err != nil {
		return credsUnusable(err)
	}
	if err := os.MkdirAll(filepath.Dir(c.blob), 0o700); err != nil {
		return err
	}
	return os.WriteFile(c.blob, sealed, 0o600)
}

func (c *credStore) Delete() error {
	if err := os.Remove(c.blob); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
