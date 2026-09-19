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
	addr, err := l.waitForAnnouncement(ctx, opts.PeerID)
	if err != nil {
		return nil, fmt.Errorf("transport: local discovery: %w", err)
	}
	raw, err := dialTCPRetrying(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("transport: local discovery: %w", err)
	}
	return doClientHandshake(l.Cert, l.logger(), raw, opts.PeerID)
}

// announceLocally multicasts an announcement of deviceID and tcpPort
// immediately and then on every announceInterval tick, until ctx is
// done or the returned stop function is called (whichever first).
// Failing to open the announcing socket is not itself an error worth
// failing Listen over: SyncthingRelay's own announce loop is running
// concurrently via Multi, so an unreachable multicast group (a network
// that genuinely blocks it) just means this Transport alone will never
// find a dialer, not that Listen as a whole cannot succeed.
func (l *LocalDiscovery) announceLocally(ctx context.Context, deviceID string, tcpPort int) func() {
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		conn, err := net.DialUDP("udp4", nil, l.groupUDPAddr())
		if err != nil {
			l.logger().Log(ctx, levelTrace, "local discovery: could not open announce socket", "error", err)
			return
		}
		defer conn.Close()

		payload, err := json.Marshal(localAnnouncement{
			Magic:    localAnnounceMagic,
			DeviceID: deviceID,
			Port:     tcpPort,
		})
		if err != nil {
			return
		}

		send := func() {
			if _, err := conn.Write(payload); err != nil {
				l.logger().Log(ctx, levelTrace, "local discovery: announce failed", "error", err)
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
	conn, err := net.ListenMulticastUDP("udp4", nil, l.groupUDPAddr())
	if err != nil {
		return "", fmt.Errorf("joining the local discovery group: %w", err)
	}
	defer conn.Close()

	// ReadFromUDP below has no ctx of its own, so cancellation is
	// implemented the same way as acceptWithContext: closing the
	// socket from a second goroutine unblocks the read. Whichever of
	// the two conn.Close() calls (this one, or the deferred one above)
	// runs second is a harmless no-op.
	stopWatching := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stopWatching:
		}
	}()
	defer func() {
		close(stopWatching)
		<-watchDone
	}()

	buf := make([]byte, 512)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("reading from the local discovery group: %w", err)
		}

		var ann localAnnouncement
		if err := json.Unmarshal(buf[:n], &ann); err != nil {
			continue
		}
		if ann.Magic != localAnnounceMagic || ann.DeviceID != peerID {
			continue
		}
		return net.JoinHostPort(src.IP.String(), strconv.Itoa(ann.Port)), nil
	}
}
