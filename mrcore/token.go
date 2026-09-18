// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"encoding/json"
	"fmt"
)

// tokenType is the LUKS2 token type value for every mr-1 token,
// distinguishing it from clevis, plain passphrase, or other token
// types that might be present on the same header.
const tokenType = "mr-1"

// currentVersion is the only version this build of mrcore can decode.
// A future incompatible protocol change bumps this and rejects older
// or newer tokens explicitly, rather than misinterpreting them.
const currentVersion = 1

// MachineInfo identifies the machine side of a token to a key holder,
// so a human can tell which machine is asking before approving an
// unlock.
type MachineInfo struct {
	Name        string `json:"name"`
	TransportID string `json:"transport_id"`
}

// Token is the LUKS2 token stored on disk for an enrolled volume.
// Nothing in it is secret; recovering P requires a key holder's
// private scalar, which never touches disk.
type Token struct {
	Version int

	// Keyslots names the LUKS2 keyslot ID(s) holding P, encrypted with
	// the LUKS2 master key derivation as usual. LUKS2 requires every
	// on-disk token to carry this field (cryptsetup's own "token
	// import" rejects a token without it, even as an empty array); it
	// is not part of the MR-1 protocol itself.
	Keyslots []string

	Recipients []Recipient
	Machine    MachineInfo

	// extra holds top-level fields this build of mrcore does not know
	// about, so a decode/encode round trip does not drop data written
	// by a newer (or differently configured) version.
	extra map[string]json.RawMessage
}

// tokenWireFields lists the field names Token understands natively;
// everything else round-trips through extra.
var tokenWireFields = map[string]bool{
	"type":       true,
	"version":    true,
	"keyslots":   true,
	"recipients": true,
	"machine":    true,
}

func (t Token) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(t.extra)+3)
	for k, v := range t.extra {
		out[k] = v
	}

	typeJSON, err := json.Marshal(tokenType)
	if err != nil {
		return nil, fmt.Errorf("mrcore: marshal token: %w", err)
	}
	out["type"] = typeJSON

	versionJSON, err := json.Marshal(t.Version)
	if err != nil {
		return nil, fmt.Errorf("mrcore: marshal token: %w", err)
	}
	out["version"] = versionJSON

	keyslots := t.Keyslots
	if keyslots == nil {
		keyslots = []string{}
	}
	keyslotsJSON, err := json.Marshal(keyslots)
	if err != nil {
		return nil, fmt.Errorf("mrcore: marshal token: %w", err)
	}
	out["keyslots"] = keyslotsJSON

	recipientsJSON, err := json.Marshal(t.Recipients)
	if err != nil {
		return nil, fmt.Errorf("mrcore: marshal token: %w", err)
	}
	out["recipients"] = recipientsJSON

	machineJSON, err := json.Marshal(t.Machine)
	if err != nil {
		return nil, fmt.Errorf("mrcore: marshal token: %w", err)
	}
	out["machine"] = machineJSON

	return json.Marshal(out)
}

func (t *Token) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("mrcore: unmarshal token: %w", err)
	}

	var typ string
	if v, ok := raw["type"]; ok {
		if err := json.Unmarshal(v, &typ); err != nil {
			return fmt.Errorf("mrcore: unmarshal token: type: %w", err)
		}
	}
	if typ != tokenType {
		return fmt.Errorf("mrcore: unmarshal token: unsupported type %q, want %q", typ, tokenType)
	}

	var version int
	if v, ok := raw["version"]; ok {
		if err := json.Unmarshal(v, &version); err != nil {
			return fmt.Errorf("mrcore: unmarshal token: version: %w", err)
		}
	}
	if version != currentVersion {
		return fmt.Errorf("mrcore: unmarshal token: unsupported version %d, want %d", version, currentVersion)
	}

	var keyslots []string
	if v, ok := raw["keyslots"]; ok {
		if err := json.Unmarshal(v, &keyslots); err != nil {
			return fmt.Errorf("mrcore: unmarshal token: keyslots: %w", err)
		}
	}

	var recipients []Recipient
	if v, ok := raw["recipients"]; ok {
		if err := json.Unmarshal(v, &recipients); err != nil {
			return fmt.Errorf("mrcore: unmarshal token: recipients: %w", err)
		}
	}

	var machine MachineInfo
	if v, ok := raw["machine"]; ok {
		if err := json.Unmarshal(v, &machine); err != nil {
			return fmt.Errorf("mrcore: unmarshal token: machine: %w", err)
		}
	}

	extra := make(map[string]json.RawMessage)
	for k, v := range raw {
		if !tokenWireFields[k] {
			extra[k] = v
		}
	}

	t.Version = version
	t.Keyslots = keyslots
	t.Recipients = recipients
	t.Machine = machine
	t.extra = extra
	return nil
}
