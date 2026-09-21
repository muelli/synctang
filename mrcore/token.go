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

// putTokenField encodes one field into the wire map, naming the field
// in any error: five copies of marshal-check-assign said nothing that
// the field name does not, and the copies did not name it.
func putTokenField(out map[string]json.RawMessage, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("mrcore: marshal token: %s: %w", key, err)
	}
	out[key] = b
	return nil
}

// getTokenField decodes one field if the document carries it, leaving
// dst alone if it does not. Absent is not an error here: which fields
// are required is decided by MarshalJSON's caller and by the explicit
// type and version checks, not by every field in turn.
func getTokenField[T any](raw map[string]json.RawMessage, key string, dst *T) error {
	v, ok := raw[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(v, dst); err != nil {
		return fmt.Errorf("mrcore: unmarshal token: %s: %w", key, err)
	}
	return nil
}

func (t Token) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(t.extra)+len(tokenWireFields))

	// Unknown fields first, so a newer build's data cannot displace a
	// field this build is responsible for.
	for k, v := range t.extra {
		out[k] = v
	}

	// LUKS2 requires a keyslots array on every token, even an empty
	// one: cryptsetup's own "token import" rejects a document without
	// it and says only "Failed to import token from file."
	keyslots := t.Keyslots
	if keyslots == nil {
		keyslots = []string{}
	}

	for _, f := range []struct {
		key   string
		value any
	}{
		{"type", tokenType},
		{"version", t.Version},
		{"keyslots", keyslots},
		{"recipients", t.Recipients},
		{"machine", t.Machine},
	} {
		if err := putTokenField(out, f.key, f.value); err != nil {
			return nil, err
		}
	}

	return json.Marshal(out)
}

func (t *Token) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("mrcore: unmarshal token: %w", err)
	}

	// Type and version first, and refused before anything else is
	// read. A LUKS2 header can carry tokens belonging to other tools
	// entirely, so the body of a document this build has no business
	// interpreting should never be parsed at all: a clevis token that
	// failed on its "recipients" field would be a confusing way to say
	// "this is not one of mine".
	var (
		typ     string
		version int
	)
	if err := getTokenField(raw, "type", &typ); err != nil {
		return err
	}
	if typ != tokenType {
		return fmt.Errorf("mrcore: unmarshal token: unsupported type %q, want %q", typ, tokenType)
	}
	if err := getTokenField(raw, "version", &version); err != nil {
		return err
	}
	if version != currentVersion {
		return fmt.Errorf("mrcore: unmarshal token: unsupported version %d, want %d", version, currentVersion)
	}

	var (
		keyslots   []string
		recipients []Recipient
		machine    MachineInfo
	)
	for _, decode := range []func() error{
		func() error { return getTokenField(raw, "keyslots", &keyslots) },
		func() error { return getTokenField(raw, "recipients", &recipients) },
		func() error { return getTokenField(raw, "machine", &machine) },
	} {
		if err := decode(); err != nil {
			return err
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
