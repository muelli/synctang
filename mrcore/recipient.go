// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import "encoding/json"

// TransportDeviceID extracts the transport-layer Device ID this Recipient
// was enrolled with, if any: the "device_id" key of r.Transport. It
// reports ("", false), never an error, when Transport is empty, is not
// valid JSON, or simply carries no device_id, since not every recipient
// type sets one (a future recipient type might not need one yet).
func (r Recipient) TransportDeviceID() (string, bool) {
	if len(r.Transport) == 0 {
		return "", false
	}

	var v struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.Unmarshal(r.Transport, &v); err != nil {
		return "", false
	}
	if v.DeviceID == "" {
		return "", false
	}
	return v.DeviceID, true
}
