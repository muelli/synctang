// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/muelli/synctang/mrcore"
)

// mrTokenType is the LUKS2 token type this project writes and looks
// for. Kept alongside cmd/unlocker's cryptsetup plumbing rather than
// in mrcore, since mrcore has no notion of LUKS2 token IDs at all.
const mrTokenType = "mr-1"

// enrolRecipient adds a new key holder to device: a fresh LUKS2
// keyslot holding a freshly generated P, and a Recipient entry
// appended to the device's single mr-1 token (created if this is the
// first enrolment). existingPassphrase must already unlock device;
// P and the concatenated stdin buffer fed to cryptsetup are wiped
// before returning, and P is never written to disk: it goes straight
// from memory into cryptsetup's stdin.
//
// transportID is this machine's own transport identity, shown to a key
// holder before it approves an unlock (mrcore.MachineInfo.TransportID).
// recipientTransportID is the opposite direction: the key holder's own
// transport Device ID, recorded on the Recipient itself so the agent
// can later restrict who is allowed to answer for it (an empty value
// is allowed; some future recipient type may not have one yet).
func enrolRecipient(device string, existingPassphrase, recipientPublicKey []byte, machineName, transportID, recipientTransportID string) error {
	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		return fmt.Errorf("reading LUKS header: %w", err)
	}

	tok, existingTokenID, err := loadToken(string(dump), device, machineName, transportID)
	if err != nil {
		return err
	}

	slot, err := lowestFreeKeyslot(string(dump))
	if err != nil {
		return fmt.Errorf("choosing a keyslot: %w", err)
	}

	P, rec, err := mrcore.Enrol(mrcore.P256(), recipientPublicKey)
	if err != nil {
		return fmt.Errorf("mrcore enrol: %w", err)
	}
	defer mrcore.Wipe(P)

	if recipientTransportID != "" {
		rec.Transport = json.RawMessage(fmt.Sprintf(`{"device_id":%q}`, recipientTransportID))
	}

	// The keyslot is added with keyMaterial, not raw P: see
	// luksKeyMaterial's own comment for why a raw random secret
	// cannot go through systemd's ask-password protocol safely at
	// recovery time. The token still stores P's own encryption (via
	// rec, from mrcore.Enrol), never keyMaterial; deriving keyMaterial
	// from the recovered P the same way at recovery time is what
	// keeps the two in agreement.
	keyMaterial := luksKeyMaterial(P)
	defer mrcore.Wipe(keyMaterial)

	stdin := make([]byte, 0, len(existingPassphrase)+len(keyMaterial))
	stdin = append(stdin, existingPassphrase...)
	stdin = append(stdin, keyMaterial...)
	defer mrcore.Wipe(stdin)

	_, err = runCommand(stdin, "cryptsetup", "luksAddKey", "--batch-mode", device,
		"--key-file=-", fmt.Sprintf("--keyfile-size=%d", len(existingPassphrase)),
		"--new-keyfile=-", fmt.Sprintf("--new-keyfile-size=%d", len(keyMaterial)),
		fmt.Sprintf("--new-key-slot=%d", slot),
	)
	if err != nil {
		return fmt.Errorf("adding keyslot: %w", err)
	}

	tok.Keyslots = append(tok.Keyslots, strconv.Itoa(slot))
	tok.Recipients = append(tok.Recipients, rec)

	tokenJSON, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("encoding token: %w", err)
	}

	if existingTokenID >= 0 {
		if _, err := runCommand(nil, "cryptsetup", "token", "remove",
			"--token-id", strconv.Itoa(existingTokenID), device); err != nil {
			return fmt.Errorf("removing the previous token version: %w", err)
		}
	}

	importArgs := []string{"token", "import"}
	if existingTokenID >= 0 {
		importArgs = append(importArgs, "--token-id", strconv.Itoa(existingTokenID))
	}
	importArgs = append(importArgs, device)
	if _, err := runCommand(tokenJSON, "cryptsetup", importArgs...); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}

	return nil
}

// removeRecipient revokes one enrolled key holder from device,
// identified by its transport Device ID (the same string
// Recipient.TransportDeviceID returns, and the one printed by
// "keyholder export-pubkey"). Both the token's Recipient entry and its
// LUKS2 keyslot are destroyed: leaving the keyslot in place would let
// whoever still holds the revoked recipient's P (or a copy of it made
// before revocation) open device directly, without going through the
// key holder or the token at all. existingPassphrase authenticates the
// keyslot removal, exactly as it does for enrolRecipient's luksAddKey.
func removeRecipient(device string, existingPassphrase []byte, recipientTransportID string) error {
	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		return fmt.Errorf("reading LUKS header: %w", err)
	}

	tok, existingTokenID, err := loadToken(string(dump), device, "", "")
	if err != nil {
		return err
	}
	if existingTokenID < 0 {
		return fmt.Errorf("%s has no enrolled mr-1 recipients", device)
	}
	if len(tok.Keyslots) != len(tok.Recipients) {
		return fmt.Errorf("token integrity check failed: %d keyslots for %d recipients, refusing to guess which is which",
			len(tok.Keyslots), len(tok.Recipients))
	}

	idx := -1
	for i, rec := range tok.Recipients {
		if id, ok := rec.TransportDeviceID(); ok && id == recipientTransportID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("no enrolled recipient with transport id %s", recipientTransportID)
	}
	keyslot := tok.Keyslots[idx]

	if _, err := runCommand(existingPassphrase, "cryptsetup", "luksKillSlot", "--batch-mode",
		device, keyslot, "--key-file=-", fmt.Sprintf("--keyfile-size=%d", len(existingPassphrase))); err != nil {
		return fmt.Errorf("destroying keyslot %s: %w", keyslot, err)
	}

	tok.Keyslots = append(tok.Keyslots[:idx], tok.Keyslots[idx+1:]...)
	tok.Recipients = append(tok.Recipients[:idx], tok.Recipients[idx+1:]...)

	tokenJSON, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("encoding token: %w", err)
	}

	if _, err := runCommand(nil, "cryptsetup", "token", "remove",
		"--token-id", strconv.Itoa(existingTokenID), device); err != nil {
		return fmt.Errorf("removing the previous token version: %w", err)
	}
	if _, err := runCommand(tokenJSON, "cryptsetup", "token", "import",
		"--token-id", strconv.Itoa(existingTokenID), device); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}

	return nil
}

// loadToken finds this device's existing mr-1 token, if any, and
// returns it along with its token ID (or -1 if there is none yet, in
// which case a fresh token carrying machineName/transportID is
// returned instead). A repeat enrolment keeps the machine identity
// the first enrolment recorded rather than the one passed this time,
// since it is the same machine asking again, not a different one.
func loadToken(dump, device, machineName, transportID string) (mrcore.Token, int, error) {
	ids := tokenIDsOfType(dump, mrTokenType)
	if len(ids) == 0 {
		return mrcore.Token{
			Version: 1,
			Machine: mrcore.MachineInfo{Name: machineName, TransportID: transportID},
		}, -1, nil
	}

	id := ids[0]
	exported, err := runCommand(nil, "cryptsetup", "token", "export",
		"--token-id", strconv.Itoa(id), device)
	if err != nil {
		return mrcore.Token{}, -1, fmt.Errorf("reading the existing token: %w", err)
	}

	var tok mrcore.Token
	if err := json.Unmarshal(exported, &tok); err != nil {
		return mrcore.Token{}, -1, fmt.Errorf("decoding the existing token: %w", err)
	}
	return tok, id, nil
}
