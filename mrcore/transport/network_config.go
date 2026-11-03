// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/syncthing/syncthing/lib/protocol"
)

// NetworkConfig is the owner's choice of relay and discovery servers,
// for someone who does not want to use the public Syncthing ones. Every
// field is optional and the zero value means the public defaults, so a
// caller with nothing configured behaves exactly as before.
//
// A machine listens, so it needs the relay and the discovery server it
// announces to; a key holder dials, so it needs the discovery server to
// look the machine up on, and then joins whichever relay the machine
// announced. Both sides must therefore agree on the discovery server.
type NetworkConfig struct {
	// RelayPool is where the listener learns relays from: an http or
	// https URL of a relay pool, or relay://host:port/?id=<relay Device
	// ID> for one relay run by the owner, which is then the only relay
	// used. Kept as given; RelayPoolURL is the form the relay client
	// takes.
	RelayPool string

	// Discovery is the global discovery server, used to look a peer up
	// and, unless DiscoveryAnnounce is set, to announce to.
	Discovery string

	// DiscoveryAnnounce is the endpoint announcements are posted to,
	// for a discovery setup where it differs from the lookup endpoint.
	DiscoveryAnnounce string
}

// The keys of a network.conf file, and of the keyholder config.
const (
	NetworkKeyRelayPool         = "relay-pool"
	NetworkKeyDiscovery         = "discovery"
	NetworkKeyDiscoveryAnnounce = "discovery-announce"
)

// ParseNetworkConfig validates the three settings of a machine, each of
// which may be empty. Errors name the setting, never echo the value: a URL
// can carry credentials, and these errors reach logs and consoles.
//
// A machine announces to its discovery server, and an announcement is never
// sent over plain http, so the discovery server must be https unless
// discovery-announce names an https endpoint of its own, when discovery is
// only what the phone is told to look up on.
func ParseNetworkConfig(relayPool, discovery, announce string) (NetworkConfig, error) {
	return parseNetworkConfig(relayPool, discovery, announce, true)
}

// ParseDialerNetworkConfig is ParseNetworkConfig for a key holder or a
// phone, which looks a machine up and never announces and has no relay
// setting (it learns the relay from discovery). Its discovery server may
// therefore be plain http, when the URL says ?insecure.
func ParseDialerNetworkConfig(discovery, announce string) (NetworkConfig, error) {
	return parseNetworkConfig("", discovery, announce, false)
}

func parseNetworkConfig(relayPool, discovery, announce string, announces bool) (NetworkConfig, error) {
	if relayPool != "" {
		if err := validateRelayPool(relayPool); err != nil {
			return NetworkConfig{}, fmt.Errorf("%s: %w", NetworkKeyRelayPool, err)
		}
	}
	if discovery != "" {
		if err := validateDiscovery(discovery, announces && announce == ""); err != nil {
			return NetworkConfig{}, fmt.Errorf("%s: %w", NetworkKeyDiscovery, err)
		}
	}
	if announce != "" {
		if err := validateDiscovery(announce, true); err != nil {
			return NetworkConfig{}, fmt.Errorf("%s: %w", NetworkKeyDiscoveryAnnounce, err)
		}
	}
	return NetworkConfig{RelayPool: relayPool, Discovery: discovery, DiscoveryAnnounce: announce}, nil
}

// validateDiscovery accepts a discovery URL (see discoveryOptions for what
// its query may say). announced is whether the URL is also announced to,
// which plain http may not be.
func validateDiscovery(s string, announced bool) error {
	_, opts, err := parseDiscoveryURL(s)
	if err != nil {
		return err
	}
	if u, err := url.Parse(s); err == nil && u.Scheme == "http" {
		switch {
		case announced:
			return errors.New("plain http is for looking up only, and this one is announced to; announcements are never sent over plain http, so use https (with ?id=<server Device ID> or ?insecure for a self-signed server)")
		case opts.hasID:
			return errors.New("?id= pins a TLS certificate, and plain http has none")
		case !opts.insecure:
			return errors.New("plain http needs ?insecure to say it is meant")
		}
	}
	return nil
}

// validateHTTPURL accepts an http or https URL with a host and no
// credentials. Used for a relay pool, which is fetched over plain http
// or https and whose answer is checked against each relay's own Device
// ID afterwards.
func validateHTTPURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("must be an http or https URL")
	}
	if u.Host == "" {
		return fmt.Errorf("has no host")
	}
	if u.User != nil {
		return fmt.Errorf("must not carry credentials")
	}
	return nil
}

func validateRelayPool(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("not a valid URL")
	}
	switch u.Scheme {
	case "relay":
		if u.User != nil {
			return fmt.Errorf("must not carry credentials")
		}
		if _, port, err := net.SplitHostPort(u.Host); err != nil || port == "" {
			return fmt.Errorf("a relay:// URL needs host and port")
		}
		// An absent id parses as the zero ID, so it is checked first.
		id := u.Query().Get("id")
		if _, err := protocol.DeviceIDFromString(id); id == "" || err != nil {
			return fmt.Errorf("a relay:// URL needs the relay's Device ID as ?id=")
		}
		return nil
	case "dynamic+http", "dynamic+https":
		u.Scheme = strings.TrimPrefix(u.Scheme, "dynamic+")
		return validateHTTPURL(u.String())
	default:
		return validateHTTPURL(s)
	}
}

// IsZero reports whether nothing is set, meaning the public defaults.
func (c NetworkConfig) IsZero() bool {
	return c == NetworkConfig{}
}

// Override returns c with every setting that o has set replacing c's.
// It is how a command line wins over a file. A discovery server that
// replaces another is a different server, so the announce endpoint that
// belonged to the old one is dropped with it (unless o names its own):
// announcing to one server and looking up on another finds nothing.
func (c NetworkConfig) Override(o NetworkConfig) NetworkConfig {
	if o.RelayPool != "" {
		c.RelayPool = o.RelayPool
	}
	if o.Discovery != "" {
		c.Discovery, c.DiscoveryAnnounce = o.Discovery, ""
	}
	if o.DiscoveryAnnounce != "" {
		c.DiscoveryAnnounce = o.DiscoveryAnnounce
	}
	return c
}

// RelayPoolURL is RelayPool in the form the relay client takes: an
// http(s) pool URL becomes dynamic+http(s), which is how that client
// tells a pool from one relay. Empty when RelayPool is.
func (c NetworkConfig) RelayPoolURL() string {
	switch {
	case c.RelayPool == "":
		return ""
	case strings.HasPrefix(c.RelayPool, "relay://"), strings.HasPrefix(c.RelayPool, "dynamic+"):
		return c.RelayPool
	default:
		return "dynamic+" + c.RelayPool
	}
}

// RelayKind says which form RelayPool was understood as, for a caller
// that wants to tell the person: "pool", "single relay", or "" when
// unset. A single relay needs its Device ID in the URL and fails
// confusingly when that is wrong, so it is worth saying out loud.
func (c NetworkConfig) RelayKind() string {
	switch {
	case c.RelayPool == "":
		return ""
	case strings.HasPrefix(c.RelayPool, "relay://"):
		return "single relay"
	default:
		return "pool"
	}
}

// NewSyncthingRelay is NewSyncthingRelay with c's settings applied.
//
// A discovery server given without an announce endpoint is announced to
// as well: leaving the public announce servers in place would publish
// this machine's relay address to exactly the servers the owner chose
// not to use, while nobody would ever look it up there.
func (c NetworkConfig) NewSyncthingRelay(cert tls.Certificate) *SyncthingRelay {
	t := NewSyncthingRelay(cert)
	t.RelayPoolURL = c.RelayPoolURL()
	t.DiscoveryURL = c.Discovery
	switch {
	case c.DiscoveryAnnounce != "":
		t.AnnounceURLs = []string{c.DiscoveryAnnounce}
	case c.Discovery != "":
		t.AnnounceURLs = []string{c.Discovery}
	}
	return t
}

// ParseNetworkConf reads a key=value file with the keys relay-pool,
// discovery and discovery-announce. Blank lines and lines starting with
// # are skipped, space around key and value is trimmed, an empty value
// means unset, and anything else (an unknown or repeated key, a line
// with no =) is an error naming the line: a typo in a file that decides
// where a machine announces itself should not be taken for "use the
// defaults".
func ParseNetworkConf(r io.Reader) (NetworkConfig, error) {
	return parseNetworkConf(r, true)
}

// ParseNetworkConfForDialer is ParseNetworkConf for a key holder's file.
// Only the machine joins a relay; a key holder learns it from discovery,
// so a relay-pool line there would do nothing, and is refused saying so
// instead of being taken for a setting that works.
func ParseNetworkConfForDialer(r io.Reader) (NetworkConfig, error) {
	return parseNetworkConf(r, false)
}

func parseNetworkConf(r io.Reader, allowRelayPool bool) (NetworkConfig, error) {
	vals := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if !ok {
			return NetworkConfig{}, fmt.Errorf("line %d: expected key=value", n)
		}
		switch key {
		case NetworkKeyRelayPool:
			if !allowRelayPool {
				return NetworkConfig{}, fmt.Errorf("line %d: %s is a machine setting; a key holder learns the relay from discovery", n, key)
			}
		case NetworkKeyDiscovery, NetworkKeyDiscoveryAnnounce:
		default:
			return NetworkConfig{}, fmt.Errorf("line %d: unknown key %q", n, key)
		}
		if _, dup := vals[key]; dup {
			return NetworkConfig{}, fmt.Errorf("line %d: %s given twice", n, key)
		}
		vals[key] = val
	}
	if err := sc.Err(); err != nil {
		return NetworkConfig{}, err
	}
	if !allowRelayPool {
		return ParseDialerNetworkConfig(vals[NetworkKeyDiscovery], vals[NetworkKeyDiscoveryAnnounce])
	}
	return ParseNetworkConfig(vals[NetworkKeyRelayPool], vals[NetworkKeyDiscovery], vals[NetworkKeyDiscoveryAnnounce])
}

// Summary says, in a line for a console, which servers will be
// used and in which form the relay setting was understood. Empty when
// everything is the public default, which needs no remark.
func (c NetworkConfig) Summary() string {
	if c.IsZero() {
		return ""
	}
	part := func(kind, value string) string {
		if value == "" {
			return kind + " public"
		}
		return kind + " " + hostOf(value)
	}
	relay := "relay public"
	if c.RelayPool != "" {
		relay = c.RelayKind() + " " + hostOf(c.RelayPool)
	}
	return fmt.Sprintf("using %s, %s", relay, part("discovery", c.Discovery))
}

// hostOf is the host (and port) of a URL, which is all a summary needs;
// the full URL, query and all, is in the file for anyone who wants it.
func hostOf(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host
	}
	return s
}
