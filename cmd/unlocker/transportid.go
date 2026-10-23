// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/syncthing/syncthing/lib/protocol"
)

// canonicalDeviceID parses a Syncthing Device ID in any shape a human
// might paste it (any case, with or without dashes, surrounding
// whitespace) and returns the canonical dashed upper-case form, which is
// what the transport reports for a connected peer and so what every
// comparison has to be made against.
func canonicalDeviceID(s string) (string, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", fmt.Errorf("an empty string is not a Device ID")
	}
	id, err := protocol.DeviceIDFromString(trimmed)
	if err != nil {
		return "", fmt.Errorf("%q is not a Device ID: %w", s, err)
	}
	return id.String(), nil
}

// canonicalOrRaw is canonicalDeviceID for values already stored in a
// token, which may have been written by hand in some other shape: one
// that does not parse is compared as it is rather than hidden.
func canonicalOrRaw(s string) string {
	if c, err := canonicalDeviceID(s); err == nil {
		return c
	}
	return s
}

// recipientTransportBlob is the opaque transport entry stored with a
// recipient: its Device ID, canonicalised, as JSON.
func recipientTransportBlob(deviceID string) (json.RawMessage, error) {
	id, err := canonicalDeviceID(deviceID)
	if err != nil {
		return nil, err
	}
	blob, err := json.Marshal(map[string]string{"device_id": id})
	if err != nil {
		return nil, err
	}
	return blob, nil
}
