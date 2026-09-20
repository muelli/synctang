// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

// defaultLocalGroupAddr is the IPv4 multicast group this project
// announces and listens on. It is deliberately not Syncthing's own
// local discovery group or port (239.21.27.1:21027): that protocol's
// wire format is different, and reusing its address would either have
// this project silently ignore genuine Syncthing traffic or, worse,
// try to parse it. 239.255.42.99 sits in the administratively scoped
// (organization-local) multicast range, meant for exactly this kind of
// private use.
const defaultLocalGroupAddr = "239.255.42.99"

// defaultLocalPort is the UDP port the multicast group above is
// announced and listened on.
const defaultLocalPort = 21076

// defaultLocalAnnounceInterval is how often Listen re-announces while
// it waits for a dialer. Much shorter than the global discovery
// default (SyncthingRelay's defaultAnnounceInterval): a LAN multicast
// packet costs nothing like a discovery server round trip does, and a
// short interval is what makes local discovery actually fast, which is
// most of the point of having it as well as working offline.
const defaultLocalAnnounceInterval = 2 * time.Second

// localAnnounceMagic distinguishes this project's announcements from
// unrelated traffic that might land on the same multicast group
// (accidentally reused elsewhere on the LAN, or simply noise); it is
// not a security boundary, since the TLS handshake that follows is
// the actual verification, exactly as with global discovery's
// unauthenticated lookups.
const localAnnounceMagic = "synctang-local-discovery-v1"

type localAnnouncement struct {
	Magic    string `json:"magic"`
	DeviceID string `json:"device_id"`
	Port     int    `json:"port"`
}

// LocalDiscovery is a Transport for the same LAN, needing no Internet
// route at all: Listen opens a plain TCP listener and periodically
// multicasts this identity's Device ID and that listener's port; Dial
// listens on the same multicast group for an announcement matching the
// Device ID it was asked to reach, then dials the announced address
// directly. The TLS/identity handshake on top is identical to
// SyncthingRelay's; only how the two sides find each other's address
// differs.
//
// This is meant to run alongside SyncthingRelay via Multi, not instead
// of it: Multi races the two, so unlock keeps working on a LAN with no
// Internet route, and still works over the Internet when there is one,
// without either side having to guess in advance which situation it is
// in.
type LocalDiscovery struct {
	// Cert is this identity's own certificate, exactly as
	// SyncthingRelay.Cert.
	Cert tls.Certificate

	// GroupAddr overrides the IPv4 multicast group address. Empty
	// means defaultLocalGroupAddr.
	GroupAddr string

	// Port overrides the UDP port the multicast group is announced
	// and listened on. Zero means defaultLocalPort.
	Port int

	// AnnounceInterval overrides how often Listen re-announces while
	// it waits. Zero means defaultLocalAnnounceInterval.
	AnnounceInterval time.Duration

	// Logger receives trace-level detail, exactly as
	// SyncthingRelay.Logger. Nil means slog's default logger.
	Logger *slog.Logger
}

func (l *LocalDiscovery) logger() *slog.Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return slog.Default()
}

func (l *LocalDiscovery) groupAddr() string {
	if l.GroupAddr != "" {
		return l.GroupAddr
	}
	return defaultLocalGroupAddr
}

func (l *LocalDiscovery) port() int {
	if l.Port != 0 {
		return l.Port
	}
	return defaultLocalPort
}

func (l *LocalDiscovery) announceInterval() time.Duration {
	if l.AnnounceInterval != 0 {
		return l.AnnounceInterval
	}
	return defaultLocalAnnounceInterval
}

func (l *LocalDiscovery) groupUDPAddr() *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP(l.groupAddr()), Port: l.port()}
}

// Listen implements Transport.
func (l *LocalDiscovery) Listen(ctx context.Context, opts ListenOptions) (Conn, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("transport: local discovery: opening a listener: %w", err)
	}
	tcpPort := ln.Addr().(*net.TCPAddr).Port

	deviceID := protocol.NewDeviceID(l.Cert.Certificate[0]).String()
	stopAnnounce := l.announceLocally(ctx, deviceID, tcpPort)
	defer stopAnnounce()

	raw, err := acceptWithContext(ctx, ln)
	if err != nil {
		return nil, fmt.Errorf("transport: local discovery: %w", err)
	}
	return doServerHandshake(l.Cert, l.logger(), raw, opts.AuthorizedPeers, nil)
}

// Dial implements Transport.
func (l *LocalDiscovery) Dial(ctx context.Context, opts DialOptions) (Conn, error) {
	// Keep listening after an announcement turns out to be useless.
	//
	// Anybody on the network can announce, and an announcement is only
	// a hint: the TLS handshake afterwards pins the Device ID, so a
	// neighbour cannot impersonate the machine. What a neighbour could
	// do, while this took only the first matching announcement and
	// gave up if it led nowhere, was announce the machine's own Device
	// ID pointing at a dead port and thereby switch off local
	// discovery altogether. That is the path that works with no
	// Internet, which is to say the one left when the relay is
	// unreachable, which is exactly when somebody is likely to be
	// interfering with the network.
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, fmt.Errorf("transport: local discovery: %w (last attempt: %v)", err, lastErr)
			}
			return nil, fmt.Errorf("transport: local discovery: %w", err)
		}

		addr, err := l.waitForAnnouncement(ctx, opts.PeerID)
		if err != nil {
			if lastErr != nil {
				return nil, fmt.Errorf("transport: local discovery: %w (last attempt: %v)", err, lastErr)
			}
			return nil, fmt.Errorf("transport: local discovery: %w", err)
		}

		// Each announced address gets its own small budget rather than
		// the caller's whole one. dialTCPRetrying keeps trying until
		// its context is done, which is right for an address this
		// machine announced itself and wrong for one a stranger sent:
		// without a bound, a single spoofed announcement consumes the
		// entire attempt and the dialler never gets back to listening.
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, announcedAddrTimeout)
		raw, err := dialTCPRetrying(attemptCtx, addr)
		cancelAttempt()
		if err != nil {
			lastErr = err
			l.logger().Log(ctx, levelTrace, "announced address did not answer, still listening",
				"addr", addr, "error", err)
			continue
		}

		conn, err := doClientHandshake(l.Cert, l.logger(), raw, opts.PeerID)
		if err != nil {
			lastErr = err
			l.logger().Log(ctx, levelTrace, "announced address was not the machine, still listening",
				"addr", addr, "error", err)
			continue
		}
		return conn, nil
	}
}

// announcedAddrTimeout bounds one attempt at an address somebody
// announced. Long enough for a machine that is still opening its
// listener, short enough that a spoofed announcement costs a moment
// rather than the whole unlock attempt.
const announcedAddrTimeout = 3 * time.Second

// multicastInterfaces returns the interfaces worth announcing on and
// listening on: up, multicast-capable, not loopback, and carrying at
// least one IPv4 address. Loopback is excluded because it is not a
// LAN, which is the only thing this Transport is about; an interface
// with no IPv4 address is excluded because there would be nothing to
// bind an "udp4" socket to.
func multicastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 ||
			ifi.Flags&net.FlagMulticast == 0 ||
			ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if interfaceIPv4(ifi) == nil {
			continue
		}
		out = append(out, ifi)
	}
	return out
}

// interfaceIPv4 returns ifi's first usable IPv4 address, or nil.
func interfaceIPv4(ifi net.Interface) net.IP {
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := ipnet.IP.To4()
		if v4 == nil || v4.IsUnspecified() {
			continue
		}
		return v4
	}
	return nil
}

// announceSockets opens one UDP socket per multicast-capable
// interface, each bound to that interface's own IPv4 address.
//
// The binding is the whole point, not an optimisation. An unbound
// socket writing to a multicast group has to find a route to it, and
// on an ordinary host nothing covers 239.0.0.0/8 except the default
// route, so a machine without one fails the write with "network is
// unreachable" and never announces at all. That is precisely the
// machine this Transport exists for: no default route means no
// Internet, which is when the relay cannot help and local discovery
// is the only path left. Binding to an interface address sends out
// that interface directly, with no route lookup to fail.
//
// Announcing on every interface rather than one also fixes the
// multi-homed case: a machine with wifi and ethernet both up has no
// way to know which side the key holder is on, and a packet on the
// wrong one is indistinguishable from no packet at all.
func (l *LocalDiscovery) announceSockets(ctx context.Context) []*net.UDPConn {
	var socks []*net.UDPConn
	for _, ifi := range multicastInterfaces() {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: interfaceIPv4(ifi)})
		if err != nil {
			l.logger().Log(ctx, levelTrace, "local discovery: could not open announce socket",
				"interface", ifi.Name, "error", err)
			continue
		}
		socks = append(socks, conn)
	}
	return socks
}

// announceLocally multicasts an announcement of deviceID and tcpPort
// immediately and then on every announceInterval tick, until ctx is
// done or the returned stop function is called (whichever first).
// Failing to open the announcing sockets is not itself an error worth
// failing Listen over: SyncthingRelay's own announce loop is running
// concurrently via Multi, so an unreachable multicast group (a network
// that genuinely blocks it) just means this Transport alone will never
// find a dialer, not that Listen as a whole cannot succeed.
func (l *LocalDiscovery) announceLocally(ctx context.Context, deviceID string, tcpPort int) func() {
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		socks := l.announceSockets(ctx)
		if len(socks) == 0 {
			l.logger().Log(ctx, levelTrace, "local discovery: no multicast-capable interface to announce on")
			return
		}
		defer func() {
			for _, s := range socks {
				s.Close()
			}
		}()

		payload, err := json.Marshal(localAnnouncement{
			Magic:    localAnnounceMagic,
			DeviceID: deviceID,
			Port:     tcpPort,
		})
		if err != nil {
			return
		}

		group := l.groupUDPAddr()
		send := func() {
			for _, s := range socks {
				if _, err := s.WriteToUDP(payload, group); err != nil {
					l.logger().Log(ctx, levelTrace, "local discovery: announce failed",
						"from", s.LocalAddr(), "error", err)
				}
			}
		}

		send()
		ticker := time.NewTicker(l.announceInterval())
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				send()
			}
		}
	}()

	return func() {
		close(stop)
		<-done
	}
}

// waitForAnnouncement listens on the local discovery multicast group
// until it sees an announcement for peerID, or ctx is done. Every
// other announcement (a different machine, or unrelated traffic that
// happens to land on the same group) is silently skipped: this is a
// rendezvous, not a directory, so there is nothing useful to do with
// an announcement for a Device ID nobody asked to reach.
func (l *LocalDiscovery) waitForAnnouncement(ctx context.Context, peerID string) (string, error) {
	conns := l.listenSockets()
	if len(conns) == 0 {
		return "", fmt.Errorf("joining the local discovery group: no multicast-capable interface")
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()

	// ReadFromUDP below has no ctx of its own, so cancellation is
	// implemented the same way as acceptWithContext: closing the
	// sockets from a second goroutine unblocks the reads. Whichever of
	// the two Close calls (this one, or the deferred one above) runs
	// second is a harmless no-op.
	stopWatching := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			for _, c := range conns {
				c.Close()
			}
		case <-stopWatching:
		}
	}()
	defer func() {
		close(stopWatching)
		<-watchDone
	}()

	// One reader per interface, first match wins. The channel is
	// buffered for every reader so that the losers, which nobody is
	// listening to any more once this function returns, do not block
	// forever on a send and leak their goroutine.
	found := make(chan string, len(conns))
	for _, c := range conns {
		go func(c *net.UDPConn) {
			if addr, ok := readAnnouncementFor(c, peerID); ok {
				found <- addr
			}
		}(c)
	}

	select {
	case addr := <-found:
		return addr, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// listenSockets joins the local discovery group on every
// multicast-capable interface. Joining on only the interface the
// kernel picks by default is not enough: a key holder laptop
// routinely has several up at once (wifi, ethernet, a container
// bridge), and listening on the wrong one is indistinguishable from
// the machine never announcing.
func (l *LocalDiscovery) listenSockets() []*net.UDPConn {
	var conns []*net.UDPConn
	for _, ifi := range multicastInterfaces() {
		conn, err := net.ListenMulticastUDP("udp4", &ifi, l.groupUDPAddr())
		if err != nil {
			continue
		}
		conns = append(conns, conn)
	}
	return conns
}

// usableAnnouncementPort reports whether a port from an announcement
// could possibly be dialled. The announcement is unauthenticated UDP
// from anybody on the network, so the number in it is whatever the
// sender felt like putting there.
func usableAnnouncementPort(port int) bool {
	return port > 0 && port <= 65535
}

// readAnnouncementFor reads from conn until it sees an announcement
// for peerID, or the socket is closed. Every other announcement (a
// different machine, or unrelated traffic that happens to land on the
// same group) is silently skipped: this is a rendezvous, not a
// directory, so there is nothing useful to do with an announcement
// for a Device ID nobody asked to reach.
func readAnnouncementFor(conn *net.UDPConn, peerID string) (string, bool) {
	buf := make([]byte, 512)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return "", false
		}

		var ann localAnnouncement
		if err := json.Unmarshal(buf[:n], &ann); err != nil {
			continue
		}
		if ann.Magic != localAnnounceMagic || ann.DeviceID != peerID {
			continue
		}
		if !usableAnnouncementPort(ann.Port) {
			continue
		}
		return net.JoinHostPort(src.IP.String(), strconv.Itoa(ann.Port)), true
	}
}
