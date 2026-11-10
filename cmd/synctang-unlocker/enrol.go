// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/muelli/synctang/mrcore"
)

// mrTokenType is the LUKS2 token type this project writes and looks
// for. Kept alongside cmd/synctang-unlocker's cryptsetup plumbing rather than
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
		return fmt.Errorf("reading LUKS header: %w%s", err, permissionHint(err))
	}

	tok, existingTokenID, err := loadToken(string(dump), device, machineName, transportID)
	if err != nil {
		return err
	}
	// An existing token keeps the name it has, but one that has none (a volume enrolled before names were
	// recorded) takes the name of this enrolment: otherwise key holders are told the machine's name is "".
	if existingTokenID >= 0 && tok.Machine.Name == "" {
		tok.Machine.Name = machineName
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
		rec.Transport, err = recipientTransportBlob(recipientTransportID)
		if err != nil {
			return fmt.Errorf("--recipient-transport-id: %w", err)
		}
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

// existingRecipient is the entry a transport id already has: where it is and
// which key it holds.
type existingRecipient struct {
	Keyslot string
	Kid     []byte
}

// enrolOutcome says what enrolOnce did.
type enrolOutcome struct {
	// Already is true when the same key was already enrolled for that
	// transport id, so nothing was added; Keyslot is where.
	Already bool
	Keyslot string
}

// keyslotOf is the keyslot of tok's idx'th recipient, or "?" when the token's two
// lists disagree about their length.
func keyslotOf(tok mrcore.Token, idx int) string {
	if idx < len(tok.Keyslots) {
		return tok.Keyslots[idx]
	}
	return "?"
}

// recipientsByTransportID is the indexes of the recipients that carry
// transport id id.
func recipientsByTransportID(tok mrcore.Token, id string) []int {
	want := canonicalOrRaw(id)
	var idxs []int
	for i, rec := range tok.Recipients {
		if got, ok := rec.TransportDeviceID(); ok && canonicalOrRaw(got) == want {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

// describeRecipients names recipients as "keyslot 1 (key abcd1234...)", for the
// messages about a transport id that is enrolled more than once.
func describeRecipients(tok mrcore.Token, idxs []int) string {
	parts := make([]string, len(idxs))
	for n, i := range idxs {
		parts[n] = fmt.Sprintf("keyslot %s (key %s)", keyslotOf(tok, i), shortKid(tok.Recipients[i].Kid))
	}
	return strings.Join(parts, ", ")
}

// enrolOnce is "enrol": it adds a key holder, but there is one enrolled
// recipient per transport id. If that id already has an entry and it holds this
// same key (the same kid), nothing is added and the outcome says where it is;
// if it holds a different key, enrolling is refused and the error says how to
// rotate instead, because a second entry for one id would leave a dead key the
// agent could pick. An empty recipientTransportID has no id to compare and is
// enrolled as before. replaceRecipient does not come through here: it adds
// the new key for the same id on purpose.
func enrolOnce(device string, existingPassphrase, recipientPublicKey []byte, machineName, transportID, recipientTransportID string) (enrolOutcome, error) {
	if recipientTransportID != "" {
		plan, err := planEnrolment(device, recipientPublicKey, recipientTransportID)
		if err != nil {
			return enrolOutcome{}, err
		}
		switch plan.Kind {
		case planAlready:
			return enrolOutcome{Already: true, Keyslot: plan.Existing.Keyslot}, nil
		case planReplace:
			return enrolOutcome{}, fmt.Errorf("transport id %s is already enrolled with a different key (keyslot %s, key %s); "+
				"to change its key use: synctang-unlocker rotate --device %s --pubkey <new public key> %s ...",
				recipientTransportID, plan.Existing.Keyslot, shortKid(plan.Existing.Kid), device, recipientTransportID)
		}
	}
	return enrolOutcome{}, enrolRecipient(device, existingPassphrase, recipientPublicKey, machineName, transportID, recipientTransportID)
}

type enrolPlanKind int

const (
	planAdd     enrolPlanKind = iota // nothing is enrolled for the id
	planAlready                      // this very key is
	planReplace                      // a different key is
)

type enrolPlan struct {
	Kind     enrolPlanKind
	Existing existingRecipient // for planAlready and planReplace
}

// planEnrolment looks at what device holds for transportID and says what
// enrolling publicKey would be. It refuses when the id is already enrolled more
// than once, which is a state to repair first (see "synctang-unlocker status").
func planEnrolment(device string, publicKey []byte, transportID string) (enrolPlan, error) {
	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		return enrolPlan{}, fmt.Errorf("reading LUKS header: %w%s", err, permissionHint(err))
	}
	tok, tokenID, err := loadToken(string(dump), device, "", "")
	if err != nil {
		return enrolPlan{}, err
	}
	if tokenID < 0 {
		return enrolPlan{Kind: planAdd}, nil
	}
	idxs := recipientsByTransportID(tok, transportID)
	switch len(idxs) {
	case 0:
		return enrolPlan{Kind: planAdd}, nil
	case 1:
		existing := existingRecipient{Keyslot: keyslotOf(tok, idxs[0]), Kid: tok.Recipients[idxs[0]].Kid}
		kid := sha256.Sum256(publicKey)
		if bytes.Equal(existing.Kid, kid[:]) {
			return enrolPlan{Kind: planAlready, Existing: existing}, nil
		}
		return enrolPlan{Kind: planReplace, Existing: existing}, nil
	default:
		return enrolPlan{}, fmt.Errorf("transport id %s is enrolled more than once (%s); remove all but one first, with "+
			"synctang-unlocker revoke --keyslot <n> (see synctang-unlocker status)",
			transportID, describeRecipients(tok, idxs))
	}
}

// removeRecipient revokes one enrolled key holder from device,
// identified by its transport Device ID (the same string
// Recipient.TransportDeviceID returns, and the one printed by
// "synctang-keyholder export-pubkey"). Both the token's Recipient entry and its
// LUKS2 keyslot are destroyed: leaving the keyslot in place would let
// whoever still holds the revoked recipient's P (or a copy of it made
// before revocation) open device directly, without going through the
// key holder or the token at all. existingPassphrase authenticates the
// keyslot removal, exactly as it does for enrolRecipient's luksAddKey.
//
// keyslot picks the entry when several share the transport id, which is how a
// duplicate left by an old bug is repaired; without it, several matches are
// refused rather than one chosen.
func removeRecipient(device string, existingPassphrase []byte, recipientTransportID, keyslot string) error {
	tok, existingTokenID, err := loadEnrolledToken(device)
	if err != nil {
		return err
	}
	idx, err := selectRecipient(tok, recipientTransportID, keyslot)
	if err != nil {
		return err
	}
	return removeRecipientAt(device, existingPassphrase, tok, existingTokenID, idx)
}

// removeRecipientByKeyslot revokes the entry that sits in keyslot, whatever its transport id,
// including none: the way to remove an entry that "revoke" cannot name by its transport id.
func removeRecipientByKeyslot(device string, existingPassphrase []byte, keyslot string) error {
	tok, tokenID, err := loadEnrolledToken(device)
	if err != nil {
		return err
	}
	for i, slot := range tok.Keyslots {
		if slot == keyslot {
			return removeRecipientAt(device, existingPassphrase, tok, tokenID, i)
		}
	}
	if len(tok.Keyslots) == 0 {
		return fmt.Errorf("keyslot %s holds no enrolled key holder: no key holder is enrolled on this device", keyslot)
	}
	where := "keyslot " + tok.Keyslots[0]
	if len(tok.Keyslots) > 1 {
		where = "keyslots " + strings.Join(tok.Keyslots, ", ")
	}
	return fmt.Errorf("keyslot %s holds no enrolled key holder (key holders are enrolled in %s)", keyslot, where)
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
		return mrcore.Token{}, -1, fmt.Errorf("reading LUKS header: %w%s", err, permissionHint(err))
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

// recipientIndexByTransportID is selectRecipient with no keyslot named.
func recipientIndexByTransportID(tok mrcore.Token, recipientTransportID string) (int, error) {
	return selectRecipient(tok, recipientTransportID, "")
}

// selectRecipient finds the one recipient with transport id recipientTransportID.
// Several matches are an error that lists them, never a choice of the first:
// removing or replacing "the first" could destroy the one entry that works.
// With keyslot set, the entry in that keyslot is chosen, if it has that id.
func selectRecipient(tok mrcore.Token, recipientTransportID, keyslot string) (int, error) {
	idxs := recipientsByTransportID(tok, recipientTransportID)
	if len(idxs) == 0 {
		return -1, fmt.Errorf("no enrolled recipient with transport id %s", recipientTransportID)
	}
	if keyslot != "" {
		for _, i := range idxs {
			if keyslotOf(tok, i) == keyslot {
				return i, nil
			}
		}
		return -1, fmt.Errorf("keyslot %s holds no entry for transport id %s (it has: %s)",
			keyslot, recipientTransportID, describeRecipients(tok, idxs))
	}
	if len(idxs) > 1 {
		return -1, fmt.Errorf("transport id %s is enrolled more than once (%s), so it cannot tell which you mean; name one with --keyslot <n>",
			recipientTransportID, describeRecipients(tok, idxs))
	}
	return idxs[0], nil
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
//
// keyslot picks the old entry when several share its transport id; see
// removeRecipient.
func replaceRecipient(device string, existingPassphrase, newPublicKey []byte, oldRecipientTransportID, newRecipientTransportID, keyslot string) error {
	tok, _, err := loadEnrolledToken(device)
	if err != nil {
		return err
	}
	idx, err := selectRecipient(tok, oldRecipientTransportID, keyslot)
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

// tokenReadAttempts and tokenReadRetryDelay bound how a read of the existing
// token is retried. cryptsetup reads a header that a concurrent "enrol" or
// "pair" may be rewriting, and a read that lands in the middle fails once and
// works a moment later ("Failed to get token 0 for export").
var (
	tokenReadAttempts   = 4
	tokenReadRetryDelay = 200 * time.Millisecond
)

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
	var exported []byte
	var err error
	for attempt := 1; attempt <= tokenReadAttempts; attempt++ {
		exported, err = runCommand(nil, "cryptsetup", "token", "export",
			"--token-id", strconv.Itoa(id), device)
		if err == nil {
			break
		}
		if attempt < tokenReadAttempts {
			time.Sleep(tokenReadRetryDelay)
		}
	}
	if err != nil {
		return mrcore.Token{}, -1, fmt.Errorf("reading the existing token: %w", err)
	}

	var tok mrcore.Token
	if err := json.Unmarshal(exported, &tok); err != nil {
		return mrcore.Token{}, -1, fmt.Errorf("decoding the existing token: %w", err)
	}
	return tok, id, nil
}
