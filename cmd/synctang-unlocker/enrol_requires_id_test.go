// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

// A key holder enrolled without its transport id can never be used: the agent tells key holders
// from strangers by that id and refuses to listen when none of the enrolled ones has one. So enrol
// refuses to make such an entry, and says where the id comes from, instead of reporting success.
func TestEnrolRefusesWithoutTheKeyHoldersTransportID(t *testing.T) {
	pass := []byte("enrol-needs-an-id")
	device := formatLoopbackImage(t, pass)
	dir := t.TempDir()
	ppFile := filepath.Join(dir, "pass")
	if err := os.WriteFile(ppFile, pass, 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := mrcore.P256().RandomScalar()
	S, _ := mrcore.P256().ScalarBaseMult(s)

	var out, errOut bytes.Buffer
	code := run([]string{"enrol", "--device", device, "--pubkey", hex.EncodeToString(S),
		"--existing-passphrase-file", ppFile, "--machine-key-file", filepath.Join(dir, "machine.pem")}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit %d, want 2:\n%s%s", code, out.String(), errOut.String())
	}
	for _, want := range []string{"--recipient-transport-id", "synctang-keyholder export-pubkey"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut.String())
		}
	}
	if strings.Contains(out.String(), "enrolled") {
		t.Errorf("it said it enrolled something:\n%s", out.String())
	}
	if got := existingMachineName(device); got != "" {
		t.Errorf("the volume was changed (token with machine name %q)", got)
	}
}
