// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/relay/client"
	socket "syncthing-socket"
)

// defaultRelayPoolURL is the public Syncthing relay pool, the same default
// syncthing-socket itself uses (see its main.go "relay" flag). It is not
// exported from that module, so it is repeated here rather than imported.
const defaultRelayPoolURL = "dynamic+https://relays.syncthing.net/endpoint"

// defaultRelayTimeout bounds a single relay protocol round trip (session
// request, join), independently of the caller's own context deadline.
const defaultRelayTimeout = 15 * time.Second

// defaultAnnounceInterval is how often SyncthingRelay.Listen refreshes its
// discovery record while it waits for a dialer. socket.DefaultAnnounceInterval
// (30 minutes) is tuned for a long-running daemon; a pairing wait is short
// and one-shot, so the record needs to appear quickly rather than merely
// stay alive, hence a much shorter default here.
const defaultAnnounceInterval = 10 * time.Second

// SyncthingRelay is a Transport built on the public Syncthing relay
// network and global discovery server: Listen connects to a relay and
// announces this identity's Device ID plus that relay's address to
// discovery, then waits for one session invitation; Dial looks the
// target Device ID up on discovery and either connects directly or joins
// the same relay session. Setting DirectAddr on either side bypasses
// both and is intended for offline tests and same-network pairing.
type SyncthingRelay struct {
	// Cert is this identity's own certificate. Callers derive it however
	// suits the situation, for example socket.GenerateDeterministicCert
	// from a one-time pairing secret, or an arbitrary persisted cert for
	// steady-state use; SyncthingRelay itself does not care.
	Cert tls.Certificate

	// RelayPoolURL overrides the relay pool to connect to. Empty means
	// defaultRelayPoolURL.
	RelayPoolURL string

	// DiscoveryURL overrides the discovery server used to announce and
	// look addresses up. Empty means socket.DefaultDiscoveryURL.
	DiscoveryURL string

	// AnnounceInterval overrides how often Listen refreshes its
	// discovery record. Zero means defaultAnnounceInterval.
	AnnounceInterval time.Duration
}

// NewSyncthingRelay returns a SyncthingRelay identified by cert, using
// the default public relay pool and discovery server.
func NewSyncthingRelay(cert tls.Certificate) *SyncthingRelay {
	return &SyncthingRelay{Cert: cert}
}

// conn adapts a *tls.Conn, once its handshake has completed and its
// peer's identity has been verified, to the Conn interface. Embedding
// *tls.Conn promotes Read, Write, Close and the rest of net.Conn.
type conn struct {
	*tls.Conn
	peerID string
}

func (c *conn) PeerID() string { return c.peerID }

// Listen implements Transport.
func (t *SyncthingRelay) Listen(ctx context.Context, opts ListenOptions) (Conn, error) {
	var raw net.Conn
	var err error
	if opts.DirectAddr != "" {
		raw, err = t.listenDirect(ctx, opts.DirectAddr)
	} else {
		raw, err = t.listenRelay(ctx)
	}
	if err != nil {
		return nil, err
	}
	return t.serverHandshake(raw, opts.AuthorizedPeers)
}

// Dial implements Transport.
func (t *SyncthingRelay) Dial(ctx context.Context, opts DialOptions) (Conn, error) {
	var raw net.Conn
	var err error
	if opts.DirectAddr != "" {
		raw, err = t.dialDirect(ctx, opts.DirectAddr)
	} else {
		raw, err = t.dialRelay(ctx, opts.PeerID)
	}
	if err != nil {
		return nil, err
	}
	return t.clientHandshake(raw, opts.PeerID)
}

// listenDirect accepts exactly one plain TCP connection on addr, or
// returns ctx's error if it is done first.
func (t *SyncthingRelay) listenDirect(ctx context.Context, addr string) (net.Conn, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("transport: direct listen on %s: %w", addr, err)
	}

	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := ln.Accept()
		ch <- result{conn, err}
	}()

	select {
	case <-ctx.Done():
		ln.Close()
		return nil, fmt.Errorf("transport: direct listen on %s: %w", addr, ctx.Err())
	case r := <-ch:
		ln.Close()
		if r.err != nil {
			return nil, fmt.Errorf("transport: direct listen on %s: %w", addr, r.err)
		}
		return r.conn, nil
	}
}

// dialDirect connects to addr, retrying until it succeeds or ctx is
// done. Retrying rather than failing on the first attempt absorbs the
// ordinary race of a dialer starting fractionally before the listener
// it is trying to reach.
func (t *SyncthingRelay) dialDirect(ctx context.Context, addr string) (net.Conn, error) {
	d := net.Dialer{}
	var lastErr error
	for {
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("transport: direct dial to %s: %w (last attempt: %v)", addr, ctx.Err(), lastErr)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// serverHandshake completes a TLS 1.3 server handshake on raw, then
// checks the peer's certificate-derived Device ID against
// authorizedPeers (skipped entirely when that list is empty). The
// check runs strictly after the handshake, against a cryptographically
// verified identity, never against anything the dialer merely claimed
// beforehand.
func (t *SyncthingRelay) serverHandshake(raw net.Conn, authorizedPeers []string) (Conn, error) {
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{t.Cert},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS13,
	}
	tlsConn := tls.Server(raw, tlsConf)
	if err := tlsConn.Handshake(); err != nil {
		raw.Close()
		return nil, fmt.Errorf("transport: server handshake: %w", err)
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		tlsConn.Close()
		return nil, fmt.Errorf("transport: peer presented no certificate")
	}
	peerID := protocol.NewDeviceID(state.PeerCertificates[0].Raw)

	if len(authorizedPeers) > 0 && !containsDeviceID(authorizedPeers, peerID) {
		tlsConn.Close()
		return nil, fmt.Errorf("transport: peer %s is not authorized", peerID)
	}

	return &conn{Conn: tlsConn, peerID: peerID.String()}, nil
}

// clientHandshake completes a TLS 1.3 client handshake on raw, then
// checks that the peer's certificate-derived Device ID matches
// expectedPeerID (skipped when that string is empty). InsecureSkipVerify
// is set because Syncthing certificates are self-signed and carry no
// certificate authority; the Device ID check immediately below is the
// real verification, exactly as syncthing-socket's own RunClient does it.
func (t *SyncthingRelay) clientHandshake(raw net.Conn, expectedPeerID string) (Conn, error) {
	tlsConf := &tls.Config{
		Certificates:       []tls.Certificate{t.Cert},
		InsecureSkipVerify: true, //nolint:gosec // verified below via the Syncthing Device ID instead.
		MinVersion:         tls.VersionTLS13,
	}
	tlsConn := tls.Client(raw, tlsConf)
	if err := tlsConn.Handshake(); err != nil {
		raw.Close()
		return nil, fmt.Errorf("transport: client handshake: %w", err)
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		tlsConn.Close()
		return nil, fmt.Errorf("transport: peer presented no certificate")
	}
	peerID := protocol.NewDeviceID(state.PeerCertificates[0].Raw)
	if expectedPeerID != "" && peerID.String() != expectedPeerID {
		tlsConn.Close()
		return nil, fmt.Errorf("transport: security mismatch: connected to %s, expected %s", peerID, expectedPeerID)
	}

	return &conn{Conn: tlsConn, peerID: peerID.String()}, nil
}

func containsDeviceID(ids []string, id protocol.DeviceID) bool {
	s := id.String()
	for _, want := range ids {
		if want == s {
			return true
		}
	}
	return false
}

func (t *SyncthingRelay) relayPoolURL() string {
	if t.RelayPoolURL != "" {
		return t.RelayPoolURL
	}
	return defaultRelayPoolURL
}

func (t *SyncthingRelay) discoveryURL() string {
	if t.DiscoveryURL != "" {
		return t.DiscoveryURL
	}
	return socket.DefaultDiscoveryURL
}

func (t *SyncthingRelay) announceInterval() time.Duration {
	if t.AnnounceInterval > 0 {
		return t.AnnounceInterval
	}
	return defaultAnnounceInterval
}

// listenRelay connects to the relay pool, announces this identity's
// address there to discovery, and returns the raw connection from the
// first session invitation it receives.
func (t *SyncthingRelay) listenRelay(ctx context.Context) (net.Conn, error) {
	u, err := url.Parse(t.relayPoolURL())
	if err != nil {
		return nil, fmt.Errorf("transport: invalid relay pool URL: %w", err)
	}

	rc, err := client.NewClient(u, []tls.Certificate{t.Cert}, defaultRelayTimeout)
	if err != nil {
		return nil, fmt.Errorf("transport: creating relay client: %w", err)
	}

	relayCtx, cancelRelay := context.WithCancel(ctx)
	defer cancelRelay()
	go rc.Serve(relayCtx) //nolint:errcheck // surfaced below via rc.Error, and relayCtx is cancelled on return.

	for rc.URI() == nil {
		if err := rc.Error(); err != nil {
			return nil, fmt.Errorf("transport: relay client: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("transport: waiting for relay connection: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}

	stopAnnounce := t.announceLoop(relayCtx, func() []string {
		if uri := rc.URI(); uri != nil {
			return []string{uri.String()}
		}
		return nil
	})
	defer stopAnnounce()

	select {
	case inv, ok := <-rc.Invitations():
		if !ok {
			return nil, fmt.Errorf("transport: relay invitations channel closed")
		}
		return client.JoinSession(ctx, inv)
	case <-ctx.Done():
		return nil, fmt.Errorf("transport: waiting for a session invitation: %w", ctx.Err())
	}
}

// dialRelay looks peerID's address up on discovery, then either dials it
// directly or joins a relay session with it, whichever the address list
// offers.
func (t *SyncthingRelay) dialRelay(ctx context.Context, peerID string) (net.Conn, error) {
	targetID, err := protocol.DeviceIDFromString(peerID)
	if err != nil {
		return nil, fmt.Errorf("transport: invalid peer Device ID %q: %w", peerID, err)
	}

	record, err := socket.LookupRecord(ctx, targetID.String(), t.discoveryURL())
	if err != nil {
		return nil, fmt.Errorf("transport: discovery lookup for %s: %w", targetID, err)
	}
	if record == nil || len(record.Addresses) == 0 {
		return nil, fmt.Errorf("transport: no addresses announced for peer %s", targetID)
	}

	var directAddrs, relayAddrs []string
	for _, addr := range record.Addresses {
		switch {
		case strings.HasPrefix(addr, "tcp://"):
			directAddrs = append(directAddrs, addr)
		case strings.HasPrefix(addr, "relay://"):
			relayAddrs = append(relayAddrs, addr)
		}
	}

	var lastErr error
	for _, addrStr := range directAddrs {
		u, err := url.Parse(addrStr)
		if err != nil {
			lastErr = err
			continue
		}
		d := net.Dialer{Timeout: 5 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", u.Host)
		if err != nil {
			lastErr = err
			continue
		}
		return conn, nil
	}

	for _, relayURI := range relayAddrs {
		u, err := url.Parse(relayURI)
		if err != nil {
			lastErr = err
			continue
		}
		inv, err := client.GetInvitationFromRelay(ctx, u, targetID, []tls.Certificate{t.Cert}, defaultRelayTimeout)
		if err != nil {
			lastErr = fmt.Errorf("relay %s: %w", u.Host, err)
			continue
		}
		conn, err := client.JoinSession(ctx, inv)
		if err != nil {
			lastErr = fmt.Errorf("joining session on relay %s: %w", u.Host, err)
			continue
		}
		return conn, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no usable direct or relay addresses for %s", targetID)
	}
	return nil, fmt.Errorf("transport: connecting to %s: %w", targetID, lastErr)
}

// announceLoop periodically POSTs addresses() to discovery, identifying
// this SyncthingRelay by its own TLS client certificate (the discovery
// server derives the announcer's Device ID from that certificate, the
// same way lookups derive a peer's Device ID from its certificate). It
// returns a function that stops the loop; callers should always call it.
func (t *SyncthingRelay) announceLoop(ctx context.Context, addresses func() []string) func() {
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(t.announceInterval())
		defer ticker.Stop()

		for {
			if err := t.announceOnce(ctx, addresses()); err != nil {
				// Best effort: a failed announce is retried on the next
				// tick rather than aborting the whole listen attempt.
				_ = err
			}
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()

	return func() {
		close(stop)
		<-done
	}
}

// announceOnce POSTs one discovery announcement, authenticated by t.Cert
// on the TLS connection to the discovery server itself (the server reads
// the announcer's identity off that certificate, not off the request
// body), mirroring syncthing-socket's own unexported announce() in spirit.
func (t *SyncthingRelay) announceOnce(ctx context.Context, addresses []string) error {
	if len(addresses) == 0 {
		return nil
	}

	payload, err := json.Marshal(struct {
		Addresses []string `json:"addresses"`
	}{Addresses: addresses})
	if err != nil {
		return fmt.Errorf("transport: marshalling announce payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.discoveryURL(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("transport: building announce request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates:       []tls.Certificate{t.Cert},
				InsecureSkipVerify: true, //nolint:gosec // this is a request to the identity's own discovery server, not to the peer; the peer is verified separately over the resulting connection.
			},
		},
		Timeout: 10 * time.Second,
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("transport: announce request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("transport: announce returned HTTP %d", resp.StatusCode)
	}
	return nil
}
