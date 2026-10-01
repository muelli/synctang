// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// defaultDiscoveryURL is the Syncthing global discovery endpoint used to
// resolve a peer's Device ID to its current addresses.
const defaultDiscoveryURL = "https://discovery-lookup.syncthing.net/v2/"

// discoveryLookupTimeout bounds one whole lookup request.
const discoveryLookupTimeout = 10 * time.Second

// discoveryRecord is what global discovery knows about a device.
//
// Seen is when the record was last refreshed, zero if the server did not
// say or said something unparseable; the record outlives the machine by
// over an hour, so its presence alone says only that the machine
// announced at some point.
type discoveryRecord struct {
	Seen      time.Time
	Addresses []string
}

// lookupRecord fetches the full discovery record for serverID from the
// discovery server at discoveryServer, timestamp included. The query is
// "?device=<id>", or "&device=<id>" if discoveryServer already carries a
// query string.
//
// A 404 is not an error: a machine that has never announced, or has been
// gone long enough for its record to expire, is the ordinary "nothing is
// waiting" case, reported as (nil, nil). Any other non-200 status is an
// error. The server's TLS certificate is verified.
func lookupRecord(ctx context.Context, serverID, discoveryServer string) (*discoveryRecord, error) {
	sep := "?"
	if strings.Contains(discoveryServer, "?") {
		sep = "&"
	}
	urlStr := discoveryServer + sep + "device=" + serverID

	client := &http.Client{Timeout: discoveryLookupTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery lookup returned HTTP %d", resp.StatusCode)
	}

	var body struct {
		Seen      string   `json:"seen"`
		Addresses []string `json:"addresses"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	record := &discoveryRecord{Addresses: body.Addresses}
	if body.Seen != "" {
		// A record we cannot date is treated as undated rather than as
		// an error: the addresses are still usable.
		if seen, err := time.Parse(time.RFC3339, body.Seen); err == nil {
			record.Seen = seen
		}
	}
	return record, nil
}
