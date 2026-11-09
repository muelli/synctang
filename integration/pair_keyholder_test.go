// SPDX-License-Identifier: AGPL-3.0-or-later

package integration

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// buildBinary builds one of this module's commands into dir and returns its path.
func buildBinary(t *testing.T, dir, command string) string {
	t.Helper()
	out := filepath.Join(dir, command)
	cmd := exec.Command("go", "build", "-o", out, "../cmd/"+command)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", command, err, b)
	}
	return out
}

// waitForOutput polls a program's output for a pattern.
func waitForOutput(t *testing.T, out *syncBuffer, re *regexp.Regexp, what string) []string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if m := re.FindStringSubmatch(out.String()); m != nil {
			return m
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no %s in:\n%s", what, out.String())
	return nil
}

// The two real programs pairing: "synctang-keyholder pair" with the link "synctang-unlocker pair"
// shows. The short code each prints has to be the same, which no test of one program alone can
// show, and the key holder has to end up enrolled and named.
func TestKeyholderPairsWithTheRealUnlocker(t *testing.T) {
	cryptsetup := ""
	for _, c := range []string{"/usr/sbin/cryptsetup", "/sbin/cryptsetup", "/usr/bin/cryptsetup"} {
		if _, err := os.Stat(c); err == nil {
			cryptsetup = c
			break
		}
	}
	if cryptsetup == "" {
		t.Skip("no cryptsetup")
	}
	dir := t.TempDir()
	unlocker := buildBinary(t, dir, "synctang-unlocker")
	keyholder := buildBinary(t, dir, "synctang-keyholder")

	pass := "pair-integration-passphrase"
	passFile := filepath.Join(dir, "pass")
	if err := os.WriteFile(passFile, []byte(pass), 0o600); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(dir, "volume.img")
	if b, err := exec.Command("truncate", "-s", "32M", image).CombinedOutput(); err != nil {
		t.Fatalf("truncate: %v %s", err, b)
	}
	format := exec.Command(cryptsetup, "luksFormat", "--type", "luks2", "--batch-mode", "--key-file=-",
		"--pbkdf", "pbkdf2", "--pbkdf-force-iterations", "1000", image)
	format.Stdin = strings.NewReader(pass)
	if b, err := format.CombinedOutput(); err != nil {
		t.Fatalf("luksFormat: %v %s", err, b)
	}

	addr := "127.0.0.1:" + strings.TrimPrefix(freeLoopbackAddr(t), "127.0.0.1:")
	machineKey := filepath.Join(dir, "machine.pem")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	machine := exec.CommandContext(ctx, unlocker, "pair", "--device", image, "--name", "integration-box",
		"--existing-passphrase-file", passFile, "--machine-key-file", machineKey, "--direct-addr", addr, "--qr", "plain")
	machineOut := &syncBuffer{}
	machine.Stdout, machine.Stderr = machineOut, machineOut
	machine.Stdin = strings.NewReader("y\n")
	if err := machine.Start(); err != nil {
		t.Fatal(err)
	}
	link := waitForOutput(t, machineOut, regexp.MustCompile(`synctang://pair\?\S+`), "pairing link")[0]

	cfg := filepath.Join(dir, "kh")
	holder := exec.CommandContext(ctx, keyholder, "pair", "--direct-addr", addr, "--timeout", "30s",
		"--store", "file", "--key-file", filepath.Join(cfg, "key"), "--transport-key-file", filepath.Join(cfg, "transport.pem"),
		"--machines-file", filepath.Join(cfg, "machines"), "--name", "integration-laptop", link)
	holderOut, err := holder.CombinedOutput()
	if err != nil {
		t.Fatalf("synctang-keyholder pair: %v\n%s\nmachine said:\n%s", err, holderOut, machineOut.String())
	}
	if err := machine.Wait(); err != nil {
		t.Fatalf("synctang-unlocker pair: %v\n%s", err, machineOut.String())
	}

	sas := regexp.MustCompile(`\b\d{3} \d{3}\b`)
	onMachine := sas.FindString(machineOut.String())
	onLaptop := sas.FindString(string(holderOut))
	if onMachine == "" || onLaptop == "" || onMachine != onLaptop {
		t.Errorf("the codes differ: machine %q, laptop %q\nmachine:\n%s\nlaptop:\n%s", onMachine, onLaptop, machineOut.String(), holderOut)
	}

	// Enrolled, and with the laptop's own transport id (which is what lets the agent answer it).
	exported, err := exec.Command(keyholder, "export-pubkey", "--key-file", filepath.Join(cfg, "key"),
		"--transport-key-file", filepath.Join(cfg, "transport.pem")).Output()
	if err != nil {
		t.Fatal(err)
	}
	id := regexp.MustCompile(`transport-id: (\S+)`).FindStringSubmatch(string(exported))
	status, err := exec.Command(unlocker, "status", "--device", image, "--machine-key-file", machineKey).CombinedOutput()
	if err != nil || id == nil || !strings.Contains(string(status), id[1]) {
		t.Errorf("the machine does not list the laptop (%v):\n%s", err, status)
	}
	if book, _ := os.ReadFile(filepath.Join(cfg, "machines")); !strings.Contains(string(book), "integration-box") {
		t.Errorf("the machine was not named:\n%s", book)
	}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}
