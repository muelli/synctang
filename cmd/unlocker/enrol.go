// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"slices"
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

	// One descriptor each, so neither secret's length has to be named
	// on the command line: cryptsetup reads each to EOF. See
	// runCommandWithSecrets.
	_, err = runCommandWithSecrets(
		[][]byte{existingPassphrase, keyMaterial},
		"cryptsetup",
		func(paths []string) []string {
			return []string{
				"luksAddKey", "--batch-mode", device,
				"--key-file=" + paths[0],
				"--new-keyfile=" + paths[1],
				fmt.Sprintf("--new-key-slot=%d", slot),
			}
		},
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
	tok, existingTokenID, err := loadEnrolledToken(device)
	if err != nil {
		return err
	}
	idx, err := recipientIndexByTransportID(tok, recipientTransportID)
	if err != nil {
		return err
	}
	return removeRecipientAt(device, existingPassphrase, tok, existingTokenID, idx)
}

// loadEnrolledToken reads device's mr-1 token and insists it actually
// has recipients, so callers that are about to destroy a keyslot never
// work from a token that was invented on the spot by loadToken. The
// keyslot/recipient integrity check belongs here for the same reason:
// the two lists are positional, and every operation below indexes one
// by the other's index.
func loadEnrolledToken(device string) (mrcore.Token, int, error) {
	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		return mrcore.Token{}, -1, fmt.Errorf("reading LUKS header: %w", err)
	}

	tok, existingTokenID, err := loadToken(string(dump), device, "", "")
	if err != nil {
		return mrcore.Token{}, -1, err
	}
	if existingTokenID < 0 {
		return mrcore.Token{}, -1, fmt.Errorf("%s has no enrolled mr-1 recipients", device)
	}
	if len(tok.Keyslots) != len(tok.Recipients) {
		return mrcore.Token{}, -1, fmt.Errorf("token integrity check failed: %d keyslots for %d recipients, refusing to guess which is which",
			len(tok.Keyslots), len(tok.Recipients))
	}
	return tok, existingTokenID, nil
}

func recipientIndexByTransportID(tok mrcore.Token, recipientTransportID string) (int, error) {
	for i, rec := range tok.Recipients {
		if id, ok := rec.TransportDeviceID(); ok && id == recipientTransportID {
			return i, nil
		}
	}
	return -1, fmt.Errorf("no enrolled recipient with transport id %s", recipientTransportID)
}

// removeRecipientAt destroys the keyslot of tok's idx'th recipient and
// drops it from the token. Split out from removeRecipient because
// replaceRecipient must revoke a specific entry rather than whichever
// one matches a transport id: after a rotation the old and new entries
// usually share one, since a key holder that rotates its MR-1 key keeps
// its transport identity.
func removeRecipientAt(device string, existingPassphrase []byte, tok mrcore.Token, tokenID, idx int) error {
	keyslot := tok.Keyslots[idx]

	if _, err := runCommandWithSecrets([][]byte{existingPassphrase}, "cryptsetup",
		func(paths []string) []string {
			return []string{"luksKillSlot", "--batch-mode", device, keyslot, "--key-file=" + paths[0]}
		}); err != nil {
		return fmt.Errorf("destroying keyslot %s: %w", keyslot, err)
	}

	tok.Keyslots = append(tok.Keyslots[:idx], tok.Keyslots[idx+1:]...)
	tok.Recipients = append(tok.Recipients[:idx], tok.Recipients[idx+1:]...)

	tokenJSON, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("encoding token: %w", err)
	}

	if _, err := runCommand(nil, "cryptsetup", "token", "remove",
		"--token-id", strconv.Itoa(tokenID), device); err != nil {
		return fmt.Errorf("removing the previous token version: %w", err)
	}
	if _, err := runCommand(tokenJSON, "cryptsetup", "token", "import",
		"--token-id", strconv.Itoa(tokenID), device); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}

	return nil
}

// replaceRecipient rotates one key holder's key: the new public key is
// enrolled and the old entry is revoked, in that order, so there is
// never a moment at which the key holder cannot unlock device. The
// reverse order would open a window in which a mistake, a crash, or a
// typo in the new public key leaves that key holder locked out of a
// volume only it was supposed to be able to open.
//
// The old entry is identified by transport id but revoked by keyslot.
// Those differ in the normal case: a key holder that rotates its MR-1
// key keeps its transport identity, so immediately after the enrolment
// two entries carry oldRecipientTransportID and "the one matching this
// id" no longer names one of them.
//
// Both entries exist between the two steps, which is the safe way round
// but does mean a failure there leaves the volume openable by either
// key. The error says so, and names the keyslot, since at that point no
// transport id distinguishes them.
func replaceRecipient(device string, existingPassphrase, newPublicKey []byte, oldRecipientTransportID, newRecipientTransportID string) error {
	tok, _, err := loadEnrolledToken(device)
	if err != nil {
		return err
	}
	idx, err := recipientIndexByTransportID(tok, oldRecipientTransportID)
	if err != nil {
		return err
	}
	oldKeyslot := tok.Keyslots[idx]

	// Machine name and transport id are left empty deliberately: this
	// device already has a token, and loadToken keeps the machine
	// identity the first enrolment recorded rather than taking a new
	// one from a later caller.
	if err := enrolRecipient(device, existingPassphrase, newPublicKey, "", "", newRecipientTransportID); err != nil {
		return fmt.Errorf("enrolling the replacement key (nothing was revoked, the old key still works): %w", err)
	}

	tok, tokenID, err := loadEnrolledToken(device)
	if err != nil {
		return fmt.Errorf("re-reading the token after enrolling the replacement key: %w", err)
	}
	idx = slices.Index(tok.Keyslots, oldKeyslot)
	if idx < 0 {
		return fmt.Errorf("the replacement key is enrolled, but keyslot %s (the old key) is no longer in the token, so it was not revoked", oldKeyslot)
	}
	if err := removeRecipientAt(device, existingPassphrase, tok, tokenID, idx); err != nil {
		return fmt.Errorf("the replacement key is enrolled and works, but revoking the old key in keyslot %s failed, so %s can currently be opened by either key: %w",
			oldKeyslot, device, err)
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
