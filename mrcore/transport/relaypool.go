// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/syncthing/syncthing/lib/osutil"
	"github.com/syncthing/syncthing/lib/relay/client"
	"github.com/syncthing/syncthing/lib/relay/protocol"
)

// Joining the public relay pool.
//
// Syncthing's own client for a pool URL ("dynamic+https://...") fetches the list,
// then measures the latency to every relay on it one after another, and only then
// joins one. The public pool lists about a hundred relays and the measurement is a
// TCP connection to each, so on a real machine that was a second to fetch the list
// and nineteen more to measure it, and the machine is not reachable until it has
// joined: a phone tapping Unlock in that time sees "Reaching the machine" and
// retries (trace of a Fedora 44 VM, 2026-11-03: relay client created 06:58:17.5,
// joined relay 06:58:37.4; the same dial with the listener already waiting took
// 1.7 s). poolClient does what that client does, in the same order, but measures
// all the relays at once.

// relayProbeTimeout bounds one latency measurement. osutil.TCPPing already gives
// up after a second; this is the bound for a probe that does not.
const relayProbeTimeout = 2 * time.Second

// relayLatencyBucket is how coarsely latency is compared, as in Syncthing's client:
// relays within the same 50 ms are equally near, and one of them is picked at
// random so the nearest few share the load.
const relayLatencyBucket = 50 * time.Millisecond

type probeFunc func(ctx context.Context, relayURL string) (time.Duration, error)

// orderRelays measures every relay at the same time, each for at most timeout, and
// returns them nearest first: the ones that answered in buckets of 50 ms, shuffled
// within a bucket, then the ones that did not. A cancelled ctx returns nil.
func orderRelays(ctx context.Context, relays []string, probe probeFunc, timeout time.Duration, shuffle func([]string)) []string {
	type result struct {
		latency time.Duration
		err     error
	}
	results := make([]result, len(relays))
	var wg sync.WaitGroup
	for i, relay := range relays {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			results[i].latency, results[i].err = probe(pctx, relay)
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil
	}

	buckets := make(map[int][]string)
	var silent []string
	for i, relay := range relays {
		if results[i].err != nil {
			silent = append(silent, relay)
			continue
		}
		id := int(results[i].latency / relayLatencyBucket)
		buckets[id] = append(buckets[id], relay)
	}
	ids := make([]int, 0, len(buckets))
	for id := range buckets {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	ordered := make([]string, 0, len(relays))
	for _, id := range ids {
		shuffle(buckets[id])
		ordered = append(ordered, buckets[id]...)
	}
	shuffle(silent)
	return append(ordered, silent...)
}

func tcpProbe(ctx context.Context, relayURL string) (time.Duration, error) {
	return osutil.GetLatencyForURL(ctx, relayURL)
}

func shuffleStrings(s []string) {
	rand.Shuffle(len(s), func(i, j int) { s[i], s[j] = s[j], s[i] })
}

// poolClient is a client.RelayClient for a "dynamic+http(s)://" pool URL. Like
// Syncthing's, it walks the pool's relays once, nearest first, joining the first
// that will have it and moving to the next when one gives up, and returns an error
// once every relay has been tried (Multi restarts a listening leg that fails).
type poolClient struct {
	pool    *url.URL
	certs   []tls.Certificate
	timeout time.Duration

	probe     probeFunc
	shuffle   func([]string)
	newStatic func(*url.URL) (client.RelayClient, error)
	http      *http.Client

	invitations chan protocol.SessionInvitation

	mu  sync.RWMutex
	cur client.RelayClient
	err error
}

func newPoolClient(pool *url.URL, certs []tls.Certificate, timeout time.Duration) *poolClient {
	c := &poolClient{
		pool:        pool,
		certs:       certs,
		timeout:     timeout,
		probe:       tcpProbe,
		shuffle:     shuffleStrings,
		http:        &http.Client{Timeout: 10 * time.Second},
		invitations: make(chan protocol.SessionInvitation),
	}
	c.newStatic = func(u *url.URL) (client.RelayClient, error) {
		return client.NewClient(u, c.certs, c.timeout)
	}
	return c
}

func (c *poolClient) Serve(ctx context.Context) error {
	err := c.serve(ctx)
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
	return err
}

func (c *poolClient) serve(ctx context.Context) error {
	poolURL := *c.pool
	poolURL.Scheme = strings.TrimPrefix(poolURL.Scheme, "dynamic+")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, poolURL.String(), nil)
	if err != nil {
		return fmt.Errorf("relay pool %s: %w", poolURL.Host, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("fetching the relay pool %s: %w", poolURL.Host, err)
	}
	var listing struct {
		Relays []struct{ URL string }
	}
	err = json.NewDecoder(resp.Body).Decode(&listing)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("reading the relay pool %s: %w", poolURL.Host, err)
	}
	var addrs []string
	for _, r := range listing.Relays {
		if u, err := url.Parse(r.URL); err == nil {
			addrs = append(addrs, u.String())
		}
	}

	for _, addr := range orderRelays(ctx, addrs, c.probe, relayProbeTimeout, c.shuffle) {
		if ctx.Err() != nil {
			return nil
		}
		u, err := url.Parse(addr)
		if err != nil {
			continue
		}
		relay, err := c.newStatic(u)
		if err != nil {
			continue
		}
		c.mu.Lock()
		c.cur = relay
		c.mu.Unlock()

		relayCtx, stopRelay := context.WithCancel(ctx)
		go c.forwardInvitations(relayCtx, relay)
		relay.Serve(relayCtx) //nolint:errcheck // the next relay is tried either way
		stopRelay()

		c.mu.Lock()
		c.cur = nil
		c.mu.Unlock()
	}
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("could not find a connectable relay")
}

// forwardInvitations hands what the current relay is invited to on, until that
// relay is given up.
func (c *poolClient) forwardInvitations(ctx context.Context, relay client.RelayClient) {
	for {
		select {
		case inv := <-relay.Invitations():
			select {
			case c.invitations <- inv:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *poolClient) Error() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cur != nil {
		return c.cur.Error()
	}
	return c.err
}

func (c *poolClient) String() string { return fmt.Sprintf("poolClient:%p:%s", c, c.pool.Host) }

func (c *poolClient) Invitations() <-chan protocol.SessionInvitation { return c.invitations }

// URI is the relay joined now, nil while between relays or before the first.
func (c *poolClient) URI() *url.URL {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cur == nil {
		return nil
	}
	return c.cur.URI()
}
