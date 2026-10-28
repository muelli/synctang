// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
)

// fakeCryptsetup puts a cryptsetup on PATH whose "token export" fails the first
// failures times and then prints a token, and returns the file that counts how
// many times it was asked.
func fakeCryptsetup(t *testing.T, failures int, token []byte) string {
	t.Helper()
	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	if err := os.WriteFile(filepath.Join(dir, "token.json"), token, 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
n=$(cat "` + counter + `" 2>/dev/null || echo 0); n=$((n + 1)); echo $n > "` + counter + `"
if [ "$n" -le ` + strconv.Itoa(failures) + ` ]; then
  echo "Failed to get token 0 for export." >&2
  exit 1
fi
cat "` + filepath.Join(dir, "token.json") + `"
`
	if err := os.WriteFile(filepath.Join(dir, "cryptsetup"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return counter
}

func callsTo(t *testing.T, counter string) int {
	t.Helper()
	b, err := os.ReadFile(counter)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func shortRetries(t *testing.T) {
	t.Helper()
	was := tokenReadRetryDelay
	tokenReadRetryDelay = time.Millisecond
	t.Cleanup(func() { tokenReadRetryDelay = was })
}

// "unlocker status" run while "unlocker pair" was writing the header failed once
// with "Failed to get token 0 for export" and worked a moment later: cryptsetup
// reads a header that a concurrent writer is rewriting. A read that fails once
// for that reason has to be tried again.
func TestReadingTheTokenIsRetriedWhileTheHeaderIsBeingRewritten(t *testing.T) {
	shortRetries(t)
	token, _ := json.Marshal(mrcore.Token{Version: 1, Machine: mrcore.MachineInfo{Name: "the-name"}})
	counter := fakeCryptsetup(t, 1, token)

	tok, id, err := loadToken(sampleDump, "/dev/fake", "", "")
	if err != nil {
		t.Fatalf("a single transient failure must not fail the read: %v", err)
	}
	if id != 2 || tok.Machine.Name != "the-name" {
		t.Errorf("got token %d named %q", id, tok.Machine.Name)
	}
	if got := callsTo(t, counter); got != 2 {
		t.Errorf("cryptsetup was asked %d times, want 2", got)
	}
}

// A header that cannot be read at all must still be an error, and not a loop.
func TestAPersistentFailureReadingTheTokenIsStillAnError(t *testing.T) {
	shortRetries(t)
	counter := fakeCryptsetup(t, 1000, []byte("{}"))

	if _, _, err := loadToken(sampleDump, "/dev/fake", "", ""); err == nil {
		t.Fatal("expected an error")
	}
	if got := callsTo(t, counter); got != tokenReadAttempts {
		t.Errorf("cryptsetup was asked %d times, want %d", got, tokenReadAttempts)
	}
}
