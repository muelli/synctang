// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
)

// The machine tells the key holder what it will do with the answer, so that the person approving is not told it
// is an unlock when it is not. It is the machine's own word, and an honest machine's: what the answer is good for
// does not change with it, which is the passphrase of the approving key holder's own keyslot.

func TestAnOrdinaryUnlockSaysNoPurpose(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := startOpen(f.args("--timeout", "20s"))
	hello, result := f.answerSeeing(t, ctx, f.s)
	if !result.OK || hello.Purpose != "" {
		t.Errorf("open: hello %+v, result %+v", hello, result)
	}
	waitFor(t, done)
}

func TestExtractingTheKeyslotKeySaysSo(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := startOpen(f.extractArgs("--timeout", "20s"))
	hello, result := f.answerSeeing(t, ctx, f.s)
	if !result.OK || hello.Purpose != mrcore.PurposeExtract {
		t.Errorf("extract: hello %+v, result %+v", hello, result)
	}
	waitFor(t, done)
}

func TestEnrollingWithAnApprovalSaysSo(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)
	pubHex, id2 := newKeyHolder(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := startOpen(f.enrolArgs(pubHex, id2, "--approve-with-key-holder", "--timeout", "20s"))
	hello, result := f.answerSeeing(t, ctx, f.s)
	if !result.OK || hello.Purpose != mrcore.PurposeEnrol {
		t.Errorf("enrol: hello %+v, result %+v", hello, result)
	}
	waitFor(t, done)
}

// pair asks for its approval through the same function; the command around it is tested with a stand-in for it.
func TestPairingWithAnApprovalSaysSo(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	type got struct {
		key  []byte
		code int
	}
	out := make(chan got, 1)
	go func() {
		key, code := keyFromApproval(f.device, approval{
			machineKeyFile: f.machineKey, directAddr: f.addr, timeout: 20 * time.Second,
			what: "pairing a key holder", purpose: mrcore.PurposePair,
		}, discard{}, discard{})
		out <- got{key, code}
	}()
	hello, result := f.answerSeeing(t, ctx, f.s)
	if !result.OK || hello.Purpose != mrcore.PurposePair {
		t.Errorf("pair: hello %+v, result %+v", hello, result)
	}
	if r := <-out; r.code != 0 || len(r.key) == 0 {
		t.Errorf("exit %d, key %d bytes", r.code, len(r.key))
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
