// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
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

// dialRelayRetryInterval is how long dialRelay waits between attempts.
// Not load-bearing; matches dialDirect's own spirit (retry rather than
// fail once), just slower, since a relay attempt is far more expensive
// than a bare TCP dial.
const dialRelayRetryInterval = 3 * time.Second

// defaultAnnounceInterval is how often SyncthingRelay.Listen refreshes its
// discovery record while it waits for a dialer. socket.DefaultAnnounceInterval
// (30 minutes) is tuned for a long-running daemon; a pairing wait is short
// and one-shot, so the record needs to appear quickly rather than merely
// stay alive, hence a much shorter default here.
const defaultAnnounceInterval = 10 * time.Second

// levelTrace sits below slog's own lowest built-in level (Debug, -4),
// for detail below what a caller's --log-level debug would want by
// default (connection lifecycle events: relay joined, invitation
// received, discovery lookup results) but that is genuinely useful
// when actually diagnosing why a connection attempt is not working.
// cmd/unlocker defines the identical constant independently rather
// than importing it from here: this package depends only on
// log/slog, and a numeric level has no reason to need a shared
// symbol across packages.
const levelTrace = slog.Level(-8)

// defaultAnnounceURLs are the public Syncthing global discovery
// announce endpoints: an authenticated POST identifying the announcer
// by its TLS client certificate, which the discovery server reads
// the Device ID off. This is deliberately not the same URL as
// socket.DefaultDiscoveryURL: that one only answers unauthenticated
// GET lookups for a peer's address, and does not accept announcements
// at all. syncthing-socket's own server mode uses exactly these two
// as its default (see its main.go "discovery" flag on the server
// command), not exported from that module, so repeated here.
var defaultAnnounceURLs = []string{
	"https://discovery-announce-v4.syncthing.net/v2/?nolookup",
	"https://discovery-announce-v6.syncthing.net/v2/?nolookup",
}

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

	// DiscoveryURL overrides the discovery server used to look a
	// peer's address up (Dial). Empty means socket.DefaultDiscoveryURL.
	// This is not used for announcing; see AnnounceURLs.
	DiscoveryURL string

	// AnnounceURLs overrides the discovery announce endpoint(s) used
	// to publish this identity's address while Listen waits. Empty
	// means defaultAnnounceURLs. Announcing and looking addresses up
	// are different Syncthing global discovery endpoints, not the
	// same URL under a different HTTP method.
	AnnounceURLs []string

	// AnnounceInterval overrides how often Listen refreshes its
	// discovery record. Zero means defaultAnnounceInterval.
	AnnounceInterval time.Duration

	// Logger receives trace-level detail on every connection attempt
	// (relay join, discovery lookups and announces, handshake
	// outcomes), for a caller that wants it. Nil means slog's default
	// logger. This package depends only on log/slog (standard
	// library, zero cost for a caller that does not want the detail,
	// and portable to contexts such as Android's gomobile bind that
	// have no systemd journal to write structured entries to at all);
	// a caller that wants those, such as cmd/unlocker, constructs its
	// own journal-backed *slog.Logger and sets it here rather than
	// this package taking on that dependency itself.
	Logger *slog.Logger

	// httpClient overrides the HTTP client announceOnce uses, for
	// tests to point it at a local httptest server with its own
	// verifiable certificate instead of skipping TLS verification
	// against the real public announce servers. Nil means the real
	// client, built fresh per call with InsecureSkipVerify (see
	// announceOnce for why that is safe here).
	httpClient *http.Client
}

func (t *SyncthingRelay) logger() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
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

	// onClose, if set, runs once Close is called: the relay path uses
	// this to keep this listener registered with the relay server for
	// as long as this connection is in use, and to only deregister it
	// once the caller is done. See listenRelay's own comment for why
	// that timing matters.
	onClose func()
}

func (c *conn) PeerID() string { return c.peerID }

func (c *conn) Close() error {
	err := c.Conn.Close()
	if c.onClose != nil {
		c.onClose()
	}
	return err
}

// Listen implements Transport.
func (t *SyncthingRelay) Listen(ctx context.Context, opts ListenOptions) (Conn, error) {
	if opts.DirectAddr != "" {
		raw, err := t.listenDirect(ctx, opts.DirectAddr)
		if err != nil {
			return nil, err
		}
		return t.serverHandshake(raw, opts.AuthorizedPeers, nil)
	}

	raw, cleanup, err := t.listenRelay(ctx)
	if err != nil {
		return nil, err
	}
	return t.serverHandshake(raw, opts.AuthorizedPeers, cleanup)
}

// Dial implements Transport.
func (t *SyncthingRelay) Dial(ctx context.Context, opts DialOptions) (Conn, error) {
	if opts.DirectAddr != "" {
		raw, err := t.dialDirect(ctx, opts.DirectAddr)
		if err != nil {
			return nil, err
		}
		return t.clientHandshake(raw, opts.PeerID)
	}
	return t.dialRelay(ctx, opts.PeerID)
}

// listenDirect accepts exactly one plain TCP connection on addr, or
// returns ctx's error if it is done first.
func (t *SyncthingRelay) listenDirect(ctx context.Context, addr string) (net.Conn, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("transport: direct listen on %s: %w", addr, err)
	}
	conn, err := acceptWithContext(ctx, ln)
	if err != nil {
		return nil, fmt.Errorf("transport: direct listen on %s: %w", addr, err)
	}
	return conn, nil
}

// acceptWithContext accepts exactly one connection from ln, closing ln
// (whether or not a connection was actually pending) before returning
// either way: a single-shot listener has nothing further to offer
// once this returns, and leaving it open would leak a socket bound to
// a port nothing is using any more. Shared by SyncthingRelay's and
// LocalDiscovery's direct-listen paths, which are otherwise identical
// down to the ctx-cancellation behaviour.
func acceptWithContext(ctx context.Context, ln net.Listener) (net.Conn, error) {
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
		return nil, ctx.Err()
	case r := <-ch:
		ln.Close()
		return r.conn, r.err
	}
}

// dialDirect connects to addr, retrying until it succeeds or ctx is
// done. Retrying rather than failing on the first attempt absorbs the
// ordinary race of a dialer starting fractionally before the listener
// it is trying to reach.
func (t *SyncthingRelay) dialDirect(ctx context.Context, addr string) (net.Conn, error) {
	return dialTCPRetrying(ctx, addr)
}

// dialTCPRetrying is dialDirect's body, shared with LocalDiscovery's
// dial path once it has learned the machine's address from a local
// announcement: that address is only ever a few milliseconds stale,
// but the listener side may not have called Accept yet, so the same
// short retry applies.
func dialTCPRetrying(ctx context.Context, addr string) (net.Conn, error) {
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
			return nil, fmt.Errorf("dial to %s: %w (last attempt: %v)", addr, ctx.Err(), lastErr)
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
// onClose, if non-nil, is attached to the returned Conn (run once the
// caller closes it) on success, and run immediately on any failure
// path here, since there is then no Conn to attach it to.
func (t *SyncthingRelay) serverHandshake(raw net.Conn, authorizedPeers []string, onClose func()) (Conn, error) {
	return doServerHandshake(t.Cert, t.logger(), raw, authorizedPeers, onClose)
}

// doServerHandshake is serverHandshake's body, a free function so
// LocalDiscovery can share it: the TLS handshake and Device ID checks
// are exactly the same regardless of how the two sides found each
// other.
func doServerHandshake(cert tls.Certificate, logger *slog.Logger, raw net.Conn, authorizedPeers []string, onClose func()) (Conn, error) {
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS13,
	}
	tlsConn := tls.Server(raw, tlsConf)
	if err := tlsConn.Handshake(); err != nil {
		raw.Close()
		if onClose != nil {
			onClose()
		}
		return nil, fmt.Errorf("transport: server handshake: %w", err)
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		tlsConn.Close()
		if onClose != nil {
			onClose()
		}
		return nil, fmt.Errorf("transport: peer presented no certificate")
	}
	peerID := protocol.NewDeviceID(state.PeerCertificates[0].Raw)
	logger.Log(context.Background(), levelTrace, "server handshake completed", "peer", peerID.String())

	if len(authorizedPeers) > 0 && !containsDeviceID(authorizedPeers, peerID) {
		tlsConn.Close()
		if onClose != nil {
			onClose()
		}
		logger.Warn("rejected an unauthorized peer", "peer", peerID.String())
		return nil, fmt.Errorf("transport: peer %s is not authorized", peerID)
	}

	return &conn{Conn: tlsConn, peerID: peerID.String(), onClose: onClose}, nil
}

// clientHandshake completes a TLS 1.3 client handshake on raw, then
// checks that the peer's certificate-derived Device ID matches
// expectedPeerID (skipped when that string is empty). InsecureSkipVerify
// is set because Syncthing certificates are self-signed and carry no
// certificate authority; the Device ID check immediately below is the
// real verification, exactly as syncthing-socket's own RunClient does it.
func (t *SyncthingRelay) clientHandshake(raw net.Conn, expectedPeerID string) (Conn, error) {
	return doClientHandshake(t.Cert, t.logger(), raw, expectedPeerID)
}

// doClientHandshake is clientHandshake's body, a free function so
// LocalDiscovery can share it.
func doClientHandshake(cert tls.Certificate, logger *slog.Logger, raw net.Conn, expectedPeerID string) (Conn, error) {
	tlsConf := &tls.Config{
		Certificates:       []tls.Certificate{cert},
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
	logger.Log(context.Background(), levelTrace, "client handshake completed", "peer", peerID.String())
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

func (t *SyncthingRelay) announceURLs() []string {
	if len(t.AnnounceURLs) > 0 {
		return t.AnnounceURLs
	}
	return defaultAnnounceURLs
}

func (t *SyncthingRelay) announceHTTPClient() *http.Client {
	if t.httpClient != nil {
		return t.httpClient
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates:       []tls.Certificate{t.Cert},
				InsecureSkipVerify: true, //nolint:gosec // this is a request to the discovery server's own announce endpoint, not to the peer; the peer is verified separately over the resulting connection.
			},
		},
		Timeout: 10 * time.Second,
	}
}

// listenRelay connects to the relay pool, announces this identity's
// address there to discovery, and returns the raw connection from the
// first session invitation it receives, along with a cleanup function
// the caller must call exactly once when it is done with that
// connection (on any error return here, listenRelay has already called
// it itself, so there is nothing left for the caller to do).
//
// The relay server drops every session belonging to a device the
// moment that device's own control connection to the relay
// disconnects (strelaysrv's listener.go calls dropSessions(id) on
// exactly that event, "to realize the client is no longer there
// faster"), including a session that only just finished being joined.
// An earlier version of this function tore its control connection
// (cancelRelay) down as soon as it had joined one session, via a plain
// defer; that raced the relay server's own cleanup against the
// still-pending TLS handshake on the connection just handed back,
// which is exactly what a real deployment's "server handshake: EOF"
// on nearly every attempt turned out to be (confirmed by reading
// strelaysrv's source after a human with hypervisor access captured
// that failure live against the actual public relay pool). The control
// connection must instead stay registered for as long as the session
// itself is in use, not merely until it is joined; the returned
// cleanup is wired into the eventual Conn's Close() by the caller, not
// invoked here on the success path.
func (t *SyncthingRelay) listenRelay(ctx context.Context) (raw net.Conn, cleanup func(), err error) {
	u, err := url.Parse(t.relayPoolURL())
	if err != nil {
		return nil, nil, fmt.Errorf("transport: invalid relay pool URL: %w", err)
	}

	rc, err := client.NewClient(u, []tls.Certificate{t.Cert}, defaultRelayTimeout)
	if err != nil {
		return nil, nil, fmt.Errorf("transport: creating relay client: %w", err)
	}
	t.logger().Log(ctx, levelTrace, "relay client created", "pool", t.relayPoolURL())

	relayCtx, cancelRelay := context.WithCancel(ctx)
	go rc.Serve(relayCtx) //nolint:errcheck // surfaced below via rc.Error, and relayCtx is cancelled by cleanup.

	// stopAnnounce is filled in once the announce loop actually starts;
	// cleanup must tolerate being called before that.
	var stopAnnounce func()
	cleanup = func() {
		if stopAnnounce != nil {
			stopAnnounce()
		}
		cancelRelay()
	}

	for rc.URI() == nil {
		if err := rc.Error(); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("transport: relay client: %w", err)
		}
		select {
		case <-ctx.Done():
			cleanup()
			return nil, nil, fmt.Errorf("transport: waiting for relay connection: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.logger().Log(ctx, levelTrace, "joined relay", "uri", rc.URI().String())

	stopAnnounce = t.announceLoop(relayCtx, func() []string {
		if uri := rc.URI(); uri != nil {
			return []string{uri.String()}
		}
		return nil
	})

	t.logger().Log(ctx, levelTrace, "waiting for a session invitation")
	select {
	case inv, ok := <-rc.Invitations():
		if !ok {
			cleanup()
			return nil, nil, fmt.Errorf("transport: relay invitations channel closed")
		}
		t.logger().Log(ctx, levelTrace, "session invitation received")
		raw, err := client.JoinSession(ctx, inv)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		return raw, cleanup, nil
	case <-ctx.Done():
		cleanup()
		return nil, nil, fmt.Errorf("transport: waiting for a session invitation: %w", ctx.Err())
	}
}

// dialRelay repeatedly looks peerID up on discovery and tries to reach
// it, until it succeeds or ctx is done. The public Syncthing relay
// pool's own connections rotate over time for reasons outside this
// project's control (individual relays disconnect clients
// periodically), which leaves discovery's address list carrying a mix
// of the listener's current relay and stale entries from ones it has
// since left; a single attempt through that list, tried once, often
// lands on a stale entry and fails even though the listener is
// genuinely reachable via a different address in the same list a few
// seconds later. dialDirect already retries for exactly this class of
// reason (see its own comment); this is the same idea applied to the
// relay path, found necessary by testing against a real deployment
// rather than assumed up front.
func (t *SyncthingRelay) dialRelay(ctx context.Context, peerID string) (Conn, error) {
	var lastErr error
	for {
		conn, err := t.dialRelayAttempt(ctx, peerID)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		t.logger().Log(ctx, levelTrace, "dial attempt failed, retrying", "peer", peerID, "error", err)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (last attempt: %v)", ctx.Err(), lastErr)
		case <-time.After(dialRelayRetryInterval):
		}
	}
}

// dialRelayAttempt is dialRelay's single attempt: one discovery
// lookup, a relay session join (or a direct dial, whichever the
// address list offers), and the TLS handshake on top of it. All three
// are retried together, not just the lookup and join: a session that
// joins successfully at the relay level can still fail its handshake
// (observed directly: the same Device ID's relay session sometimes
// joins fine and then the handshake gets EOF), and that is just as
// much a sign of "this attempt did not pan out, try a fresh one" as a
// lookup or join failure is.
func (t *SyncthingRelay) dialRelayAttempt(ctx context.Context, peerID string) (Conn, error) {
	raw, err := t.dialRelayOnce(ctx, peerID)
	if err != nil {
		return nil, err
	}
	return t.clientHandshake(raw, peerID)
}

// dialRelayOnce is dialRelayAttempt's connection-establishment half:
// one discovery lookup, then either a direct dial or a relay session
// join, whichever the address list offers.
func (t *SyncthingRelay) dialRelayOnce(ctx context.Context, peerID string) (net.Conn, error) {
	targetID, err := protocol.DeviceIDFromString(peerID)
	if err != nil {
		return nil, fmt.Errorf("transport: invalid peer Device ID %q: %w", peerID, err)
	}

	t.logger().Log(ctx, levelTrace, "looking peer up on discovery", "peer", targetID.String(), "url", t.discoveryURL())
	record, err := socket.LookupRecord(ctx, targetID.String(), t.discoveryURL())
	if err != nil {
		return nil, fmt.Errorf("transport: discovery lookup for %s: %w", targetID, err)
	}
	if record == nil || len(record.Addresses) == 0 {
		return nil, fmt.Errorf("transport: no addresses announced for peer %s", targetID)
	}
	t.logger().Log(ctx, levelTrace, "discovery lookup returned addresses", "peer", targetID.String(), "addresses", strings.Join(record.Addresses, ","))

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
				// Logged, not discarded: a silently-forever-failing
				// announce is indistinguishable from a silently
				// succeeding one from the console, which is exactly
				// how this used to POST to the wrong endpoint for a
				// long time before anyone noticed.
				t.logger().Warn("announce failed", "error", err)
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

// announceOnce POSTs one discovery announcement to every configured
// announce URL, authenticated by t.Cert on the TLS connection to each
// discovery server itself (the server reads the announcer's identity
// off that certificate, not off the request body), mirroring
// syncthing-socket's own unexported announce() in spirit. It succeeds
// if any URL accepts the announcement (a machine may only have IPv4
// or only IPv6 connectivity to the two defaults), and fails only if
// every one of them does.
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

	httpClient := t.announceHTTPClient()

	var lastErr error
	for _, announceURL := range t.announceURLs() {
		if err := t.postAnnounce(ctx, httpClient, announceURL, payload); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("transport: announce failed on every URL, last error: %w", lastErr)
}

func (t *SyncthingRelay) postAnnounce(ctx context.Context, httpClient *http.Client, announceURL string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, announceURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("building announce request to %s: %w", announceURL, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("announce request to %s: %w", announceURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("announce to %s returned HTTP %d", announceURL, resp.StatusCode)
	}
	return nil
}
