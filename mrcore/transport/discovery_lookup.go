// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/muelli/syncthing-socket/discovery"
	"github.com/syncthing/syncthing/lib/protocol"
)

// lookupTimeout bounds a whole lookup request, on top of any context
// deadline. The same number syncthing-socket's own lookup uses.
const lookupTimeout = 10 * time.Second

// discoveryOptions is how a discovery URL says whether to trust the server,
// in Syncthing's own syntax (lib/discover/global.go, parseOptions):
//
//	?id=<Device ID>  the server's certificate must be this Device ID; no
//	                 certificate authority is involved, so a self-signed one
//	                 is fine, and it is checked on every connection
//	?insecure        do not verify the server's certificate at all
//	neither          verify against the system's certificate authorities
//
// Both are for the client: they are taken out of the URL before anything is
// sent, as Syncthing does, because the server has no use for them.
type discoveryOptions struct {
	id       protocol.DeviceID
	hasID    bool
	insecure bool
}

// queryBool reads a query parameter as Syncthing does: present means true,
// unless it is exactly "false".
func queryBool(q url.Values, key string) bool {
	if _, ok := q[key]; !ok {
		return false
	}
	return q.Get(key) != "false"
}

// parseDiscoveryURL splits a discovery URL into the URL to send requests to
// and the trust options. It checks that the URL is http or https and that an
// id is a Device ID; whether plain http is acceptable depends on what the URL
// is for, which is the caller's to decide.
func parseDiscoveryURL(s string) (server string, opts discoveryOptions, err error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", opts, errors.New("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", opts, errors.New("must be an http or https URL")
	}
	if u.Host == "" {
		return "", opts, errors.New("has no host")
	}
	if u.User != nil {
		return "", opts, errors.New("must not carry credentials")
	}
	q := u.Query()
	if raw := q.Get("id"); q.Has("id") {
		id, err := protocol.DeviceIDFromString(raw)
		if raw == "" || err != nil {
			return "", opts, errors.New("?id= must be the server's Device ID")
		}
		opts.id, opts.hasID = id, true
	}
	opts.insecure = queryBool(q, "insecure")
	q.Del("id")
	q.Del("insecure")
	u.RawQuery = q.Encode()
	return u.String(), opts, nil
}

// httpClient builds the client a lookup uses. Without options it verifies
// against the system's authorities, as it always did. With an id it accepts
// exactly the certificate whose Device ID that is, and nothing else, however
// it is signed. VerifyConnection is used rather than VerifyPeerCertificate
// because only the former also runs when a session is resumed.
func (o discoveryOptions) httpClient() *http.Client {
	conf := &tls.Config{MinVersion: tls.VersionTLS12}
	o.applyTrust(conf)
	return &http.Client{
		Timeout:   lookupTimeout,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: conf},
	}
}

// applyTrust sets conf to verify the server as the options say.
func (o discoveryOptions) applyTrust(conf *tls.Config) {
	switch {
	case o.hasID:
		conf.InsecureSkipVerify = true //nolint:gosec // replaced by the Device ID check in VerifyConnection.
		conf.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("discovery server presented no certificate")
			}
			got := protocol.NewDeviceID(cs.PeerCertificates[0].Raw)
			if !got.Equals(o.id) {
				return fmt.Errorf("discovery server id mismatch: it presented Device ID %s, the URL pins %s", got, o.id)
			}
			return nil
		}
	case o.insecure:
		conf.InsecureSkipVerify = true //nolint:gosec // asked for by ?insecure in the discovery URL.
	}
}

// lookupRecord asks the discovery server at discoveryURL (with its trust
// options, see discoveryOptions) for what it knows of deviceID. It replaces
// discovery.LookupRecord, which can only verify against the system's
// authorities, and returns the same thing: nil with no error when the server
// answers 404 (never announced, or expired), and the record with its Seen
// timestamp otherwise.
func lookupRecord(ctx context.Context, deviceID, discoveryURL string) (*discovery.Record, error) {
	server, opts, err := parseDiscoveryURL(discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("discovery URL: %w", err)
	}
	u, err := url.Parse(server)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("device", deviceID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := opts.httpClient().Do(req)
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
	record := &discovery.Record{Addresses: body.Addresses}
	if body.Seen != "" {
		// A record that cannot be dated is undated, not an error: the
		// addresses are still usable. As in syncthing-socket.
		if seen, err := time.Parse(time.RFC3339, body.Seen); err == nil {
			record.Seen = seen
		}
	}
	return record, nil
}
