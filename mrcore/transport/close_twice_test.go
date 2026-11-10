// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"testing"

	"github.com/muelli/syncthing-socket/identity"
)

// A connection is closed more than once in ordinary use: by a timer that gives up on a silent peer, by the end
// of a window, and by the deferred Close of whoever holds it. The second Close must not run what the first
// one ran, or the relay's announce loop is stopped twice ("close of closed channel", which was how the
// unlocker crashed while it waited for a key holder to pair).
func TestAConnectionClosedTwiceCleansUpOnce(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	runs := 0
	c := &conn{Conn: tls.Client(a, &tls.Config{InsecureSkipVerify: true}), onClose: func() { runs++ }}
	_ = c.Close()
	_ = c.Close()
	if runs != 1 {
		t.Errorf("the cleanup ran %d times", runs)
	}
}

func TestAConnectionClosedFromTwoGoroutinesCleansUpOnce(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	var mu sync.Mutex
	runs := 0
	c := &conn{Conn: tls.Client(a, &tls.Config{InsecureSkipVerify: true}), onClose: func() { mu.Lock(); runs++; mu.Unlock() }}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Close() }()
	}
	wg.Wait()
	if runs != 1 {
		t.Errorf("the cleanup ran %d times", runs)
	}
}

func TestStoppingTheAnnounceLoopTwiceIsHarmless(t *testing.T) {
	cert, err := identity.GenerateDeterministicCert("announce stop twice")
	if err != nil {
		t.Fatal(err)
	}
	stop := NewSyncthingRelay(cert).announceLoop(context.Background(), func() []string { return nil })
	stop()
	stop()
}
