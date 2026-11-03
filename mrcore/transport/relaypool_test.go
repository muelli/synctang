// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/relay/client"
	"github.com/syncthing/syncthing/lib/relay/protocol"
)

// The public pool lists about a hundred relays. Syncthing's own client measures
// them one after another before it joins any, which took 20 seconds on a real
// machine (pool fetch 1 s, then 19 s of probing), and a machine is not reachable
// until it has joined one. These tests pin the replacement: every relay is
// measured at the same time, the nearest bucket is tried first, and one that
// does not answer costs the machine nothing but its place at the back.

func relayURLs(n int) []string {
	urls := make([]string, n)
	for i := range urls {
		urls[i] = fmt.Sprintf("relay://10.0.0.%d:22067/?id=ID%d", i, i)
	}
	return urls
}

func noShuffle([]string) {}

func TestOrderRelaysMeasuresThemAllAtOnce(t *testing.T) {
	urls := relayURLs(60)
	slow := func(ctx context.Context, _ string) (time.Duration, error) {
		select {
		case <-time.After(200 * time.Millisecond):
			return 200 * time.Millisecond, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	start := time.Now()
	got := orderRelays(context.Background(), urls, slow, time.Second, noShuffle)
	// One after another this is twelve seconds.
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("measuring 60 relays took %v: they were measured one at a time", took)
	}
	if len(got) != len(urls) {
		t.Fatalf("got %d relays, want %d", len(got), len(urls))
	}
}

func TestOrderRelaysPutsTheNearestFirstAndTheSilentLast(t *testing.T) {
	latency := map[string]time.Duration{
		"relay://a/?id=A": 10 * time.Millisecond,
		"relay://b/?id=B": 320 * time.Millisecond,
		"relay://c/?id=C": 60 * time.Millisecond,
		"relay://d/?id=D": 40 * time.Millisecond,
	}
	probe := func(_ context.Context, u string) (time.Duration, error) {
		if d, ok := latency[u]; ok {
			return d, nil
		}
		return 0, errors.New("unreachable")
	}
	in := []string{"relay://dead/?id=X", "relay://b/?id=B", "relay://c/?id=C", "relay://a/?id=A", "relay://d/?id=D"}
	got := orderRelays(context.Background(), in, probe, time.Second, noShuffle)

	// Rounded down to 50 ms buckets as Syncthing's own client does: a (10) and d (40)
	// share the first, c (60) is in the second, b (320) in the seventh, and the relay
	// that never answered comes after every one that did.
	if got[len(got)-1] != "relay://dead/?id=X" {
		t.Fatalf("the relay that did not answer is not last: %v", got)
	}
	first := map[string]bool{got[0]: true, got[1]: true}
	if !first["relay://a/?id=A"] || !first["relay://d/?id=D"] {
		t.Fatalf("the nearest bucket is not first: %v", got)
	}
	if got[2] != "relay://c/?id=C" || got[3] != "relay://b/?id=B" {
		t.Fatalf("buckets out of order: %v", got)
	}
}

func TestOrderRelaysGivesUpOnASlowRelayAtTheTimeout(t *testing.T) {
	probe := func(ctx context.Context, u string) (time.Duration, error) {
		if u == "relay://stuck/?id=S" {
			<-ctx.Done() // never answers
			return 0, ctx.Err()
		}
		return 5 * time.Millisecond, nil
	}
	start := time.Now()
	got := orderRelays(context.Background(), []string{"relay://stuck/?id=S", "relay://ok/?id=O"}, probe, 300*time.Millisecond, noShuffle)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("waited %v for a relay that never answers", took)
	}
	if got[0] != "relay://ok/?id=O" || got[1] != "relay://stuck/?id=S" {
		t.Fatalf("got %v", got)
	}
}

func TestOrderRelaysReturnsAtOnceWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	probe := func(ctx context.Context, _ string) (time.Duration, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	done := make(chan []string, 1)
	go func() { done <- orderRelays(ctx, relayURLs(10), probe, time.Minute, noShuffle) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("a cancelled ordering returned %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("orderRelays did not return after its context was cancelled")
	}
}

// fakeRelay stands in for one static relay client.
type fakeRelay struct {
	uri         *url.URL
	serveErr    error         // what Serve returns when it ends
	serveFor    time.Duration // how long Serve blocks before ending (0: until ctx is done)
	invitations chan protocol.SessionInvitation
	served      atomic.Bool
	mu          sync.Mutex
	err         error
}

func (f *fakeRelay) Serve(ctx context.Context) error {
	f.served.Store(true)
	if f.serveFor > 0 {
		select {
		case <-time.After(f.serveFor):
		case <-ctx.Done():
		}
	} else {
		<-ctx.Done()
	}
	f.mu.Lock()
	f.err = f.serveErr
	f.mu.Unlock()
	return f.serveErr
}
func (f *fakeRelay) Error() error                                   { f.mu.Lock(); defer f.mu.Unlock(); return f.err }
func (f *fakeRelay) String() string                                 { return "fake " + f.uri.Host }
func (f *fakeRelay) Invitations() <-chan protocol.SessionInvitation { return f.invitations }
func (f *fakeRelay) URI() *url.URL                                  { return f.uri }

func poolServer(t *testing.T, relays ...string) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"relays":[`)
		for i, r := range relays {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"url":%q}`, r)
		}
		fmt.Fprint(w, `]}`)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	u.Scheme = "dynamic+" + u.Scheme
	return u
}

func newTestPoolClient(pool *url.URL, make func(*url.URL) client.RelayClient) *poolClient {
	c := newPoolClient(pool, []tls.Certificate{}, time.Second)
	c.probe = func(context.Context, string) (time.Duration, error) { return time.Millisecond, nil }
	c.shuffle = noShuffle
	c.newStatic = func(u *url.URL) (client.RelayClient, error) { return make(u), nil }
	return c
}

func TestPoolClientJoinsTheFirstRelayAndReportsIt(t *testing.T) {
	pool := poolServer(t, "relay://one:22067/?id=ONE", "relay://two:22067/?id=TWO")
	var made []string
	c := newTestPoolClient(pool, func(u *url.URL) client.RelayClient {
		made = append(made, u.Host)
		return &fakeRelay{uri: u, invitations: make(chan protocol.SessionInvitation)}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Serve(ctx) //nolint:errcheck

	deadline := time.Now().Add(3 * time.Second)
	for c.URI() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.URI() == nil || c.URI().Host != "one:22067" {
		t.Fatalf("URI = %v, want the first relay", c.URI())
	}
	if len(made) != 1 {
		t.Fatalf("made %d relay clients, want 1: %v", len(made), made)
	}
}

func TestPoolClientMovesToTheNextRelayWhenOneGivesUp(t *testing.T) {
	pool := poolServer(t, "relay://one:22067/?id=ONE", "relay://two:22067/?id=TWO")
	c := newTestPoolClient(pool, func(u *url.URL) client.RelayClient {
		f := &fakeRelay{uri: u, invitations: make(chan protocol.SessionInvitation)}
		if u.Host == "one:22067" {
			f.serveErr = errors.New("connection refused")
			f.serveFor = 20 * time.Millisecond
		}
		return f
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Serve(ctx) //nolint:errcheck

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (c.URI() == nil || c.URI().Host != "two:22067") {
		time.Sleep(10 * time.Millisecond)
	}
	if c.URI() == nil || c.URI().Host != "two:22067" {
		t.Fatalf("URI = %v, want the second relay after the first gave up", c.URI())
	}
}

func TestPoolClientPassesInvitationsOn(t *testing.T) {
	pool := poolServer(t, "relay://one:22067/?id=ONE")
	relay := &fakeRelay{invitations: make(chan protocol.SessionInvitation, 1)}
	c := newTestPoolClient(pool, func(u *url.URL) client.RelayClient {
		relay.uri = u
		return relay
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Serve(ctx) //nolint:errcheck

	relay.invitations <- protocol.SessionInvitation{Key: []byte("k")}
	select {
	case inv := <-c.Invitations():
		if string(inv.Key) != "k" {
			t.Fatalf("got %q", inv.Key)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the invitation was not passed on")
	}
}

func TestPoolClientSaysSoWhenNoRelayCanBeJoined(t *testing.T) {
	pool := poolServer(t, "relay://one:22067/?id=ONE")
	c := newTestPoolClient(pool, func(u *url.URL) client.RelayClient {
		return &fakeRelay{uri: u, invitations: make(chan protocol.SessionInvitation), serveErr: errors.New("refused"), serveFor: time.Millisecond}
	})
	err := c.Serve(context.Background())
	if err == nil || c.Error() == nil {
		t.Fatalf("Serve = %v, Error = %v: want a clear failure once every relay has been tried", err, c.Error())
	}
}

func TestPoolClientReportsAPoolThatCannotBeFetched(t *testing.T) {
	u, _ := url.Parse("dynamic+http://127.0.0.1:1/endpoint") // nothing listens there
	c := newTestPoolClient(u, func(u *url.URL) client.RelayClient { return &fakeRelay{uri: u} })
	if err := c.Serve(context.Background()); err == nil {
		t.Fatal("an unreachable pool was not an error")
	}
}
