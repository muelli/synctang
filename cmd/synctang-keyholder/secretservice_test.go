// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestSecretServiceStoreSavesAndLoadsTheScalar(t *testing.T) {
	keyring.MockInit()
	st := newSecretServiceStore(filepath.Join(t.TempDir(), "key"))
	if err := st.Save(scalar32(9), false); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load()
	if err != nil || !bytes.Equal(got, scalar32(9)) {
		t.Fatalf("%x %v", got, err)
	}
	if ok, err := st.Exists(); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
}

func TestSecretServiceStoreKeepsTheScalarAsHexInItsItem(t *testing.T) {
	keyring.MockInit()
	keyFile := filepath.Join(t.TempDir(), "key")
	st := newSecretServiceStore(keyFile)
	_ = st.Save(scalar32(0xab), false)
	stored, err := keyring.Get(secretServiceName, st.account)
	if err != nil || stored != strings.Repeat("ab", 32) {
		t.Fatalf("item = %q, %v", stored, err)
	}
	if !filepath.IsAbs(st.account) {
		t.Errorf("the account %q should be the absolute path of the key file, so that two key files are two items", st.account)
	}
}

func TestSecretServiceStoreRefusesToOverwriteUnlessAsked(t *testing.T) {
	keyring.MockInit()
	st := newSecretServiceStore(filepath.Join(t.TempDir(), "key"))
	_ = st.Save(scalar32(1), false)
	if err := st.Save(scalar32(2), false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a refusal that names --force", err)
	}
	if got, _ := st.Load(); !bytes.Equal(got, scalar32(1)) {
		t.Fatal("a refused save changed the key")
	}
	if err := st.Save(scalar32(2), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Load(); !bytes.Equal(got, scalar32(2)) {
		t.Fatal("--force did not replace the key")
	}
}

func TestTwoKeyFilesAreTwoItems(t *testing.T) {
	keyring.MockInit()
	a := newSecretServiceStore(filepath.Join(t.TempDir(), "key"))
	b := newSecretServiceStore(filepath.Join(t.TempDir(), "key"))
	_ = a.Save(scalar32(1), false)
	if ok, _ := b.Exists(); ok {
		t.Fatal("another key file's key is not this one's")
	}
	_ = b.Save(scalar32(2), false)
	if got, _ := a.Load(); !bytes.Equal(got, scalar32(1)) {
		t.Fatal("saving the second key changed the first")
	}
}

func TestSecretServiceStoreSaysThereIsNoKeyWhenThereIsNone(t *testing.T) {
	keyring.MockInit()
	st := newSecretServiceStore(filepath.Join(t.TempDir(), "key"))
	if _, err := st.Load(); !errors.Is(err, errNoKey) {
		t.Fatalf("err = %v, want errNoKey", err)
	}
	if ok, err := st.Exists(); err != nil || ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if err := st.Delete(); err != nil {
		t.Fatalf("deleting what is not there is not an error: %v", err)
	}
}

func TestAKeyringThatIsNotThereIsNotMistakenForNoKey(t *testing.T) {
	keyring.MockInitWithError(errors.New("The name org.freedesktop.secrets was not provided by any .service files"))
	st := newSecretServiceStore(filepath.Join(t.TempDir(), "key"))
	_, err := st.Load()
	if err == nil || errors.Is(err, errNoKey) {
		t.Fatalf("err = %v: an unreachable keyring is not an absent key", err)
	}
	if !strings.Contains(err.Error(), "Secret Service") || !strings.Contains(err.Error(), "session") {
		t.Errorf("the error should say what to look for: %v", err)
	}
}

func TestTheAvailabilityProbeTellsAKeyringFromNone(t *testing.T) {
	keyring.MockInit()
	if err := secretServiceAvailable(); err != nil {
		t.Errorf("a working keyring is available: %v", err)
	}
	keyring.MockInitWithError(errors.New("no bus"))
	if err := secretServiceAvailable(); err == nil {
		t.Error("a keyring that errors is not available")
	}
}

func TestSecretServiceStoreDescribesItselfWithoutOverclaiming(t *testing.T) {
	st := newSecretServiceStore("/x/key")
	if st.Name() != "secret-service" {
		t.Errorf("name %q", st.Name())
	}
	d := st.Describe()
	for _, want := range []string{"keyring", "login", "software running as you", "ssh"} {
		if !strings.Contains(d, want) {
			t.Errorf("the description lacks %q: %s", want, d)
		}
	}
	if strings.Contains(strings.ToLower(d), "secure") {
		t.Errorf("a store is never called secure: %s", d)
	}
}

func TestARecordCanNameTheSecretServiceStore(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := writeStoreRecord(keyFile, "secret-service"); err != nil {
		t.Fatal(err)
	}
	st, err := openStore(keyFile)
	if err != nil || st.Name() != "secret-service" {
		t.Fatalf("%v, %v", st, err)
	}
}

// The mock proves our side of the contract; this proves the library's, against a real Secret
// Service: a private session bus and gnome-keyring-daemon, both started here, with a login
// keyring made with a password of this test's own. The library cannot go back from its mock to
// the real provider within a process, so the checks run in a child process of this test binary
// (TestRealKeyringChild). Skipped where either program is not installed.
func TestSecretServiceStoreAgainstARealKeyring(t *testing.T) {
	dbus, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("no dbus-daemon")
	}
	gkd, err := exec.LookPath("gnome-keyring-daemon")
	if err != nil {
		t.Skip("no gnome-keyring-daemon")
	}
	secretTool, err := exec.LookPath("secret-tool")
	if err != nil {
		t.Skip("no secret-tool (libsecret-tools), which makes the default keyring a login would have made")
	}
	home := t.TempDir()
	env := append(os.Environ(),
		"HOME="+home, "XDG_DATA_HOME="+filepath.Join(home, "data"), "XDG_RUNTIME_DIR="+home,
		"SYNCTANG_REAL_KEYRING_CHILD=1")

	// A bus with no services to activate: with the system's session configuration a request for
	// the Secret Service can start a second, locked daemon beside the one started here with the
	// login password, and which of the two answers is a race.
	busConf := filepath.Join(home, "bus.conf")
	if err := os.WriteFile(busConf, []byte(`<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN" "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:tmpdir=/tmp</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>`), 0o600); err != nil {
		t.Fatal(err)
	}
	bus := exec.Command(dbus, "--config-file="+busConf, "--nofork", "--print-address=1")
	out, err := bus.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Process.Kill(); _ = bus.Wait() })
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	env = append(env, "DBUS_SESSION_BUS_ADDRESS="+strings.TrimSpace(addr))

	daemon := exec.Command(gkd, "--unlock", "--components=secrets", "--foreground")
	daemon.Env = env
	daemon.Stdin = strings.NewReader("a-test-login-password")
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })

	// A desktop login makes the default keyring; a bare daemon makes it when something first
	// stores an item, so this does, once the daemon answers.
	made := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		mk := exec.Command(secretTool, "store", "--label=test setup", "setup", "probe")
		mk.Env = env
		mk.Stdin = strings.NewReader("v")
		if mk.Run() == nil {
			made = true
			break
		}
	}
	if !made {
		t.Skip("the keyring daemon did not come up")
	}

	child := exec.Command(os.Args[0], "-test.run=^TestRealKeyringChild$", "-test.v")
	child.Env = env
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("the child process failed: %v\n%s", err, out)
	} else if !strings.Contains(string(out), "PASS") || strings.Contains(string(out), "SKIP") {
		t.Fatalf("the child did not run its checks:\n%s", out)
	}
}

func TestRealKeyringChild(t *testing.T) {
	if os.Getenv("SYNCTANG_REAL_KEYRING_CHILD") == "" {
		t.Skip("run by TestSecretServiceStoreAgainstARealKeyring")
	}
	deadline := time.Now().Add(30 * time.Second)
	for secretServiceAvailable() != nil {
		if time.Now().After(deadline) {
			t.Fatalf("the keyring did not come up: %v", secretServiceAvailable())
		}
		time.Sleep(200 * time.Millisecond)
	}
	st := newSecretServiceStore(filepath.Join(t.TempDir(), "key"))
	if err := st.Save(scalar32(0x5a), false); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load()
	if err != nil || !bytes.Equal(got, scalar32(0x5a)) {
		t.Fatalf("%x %v", got, err)
	}
	if err := st.Save(scalar32(1), false); err == nil {
		t.Error("overwrote an existing key")
	}
	if err := st.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load(); !errors.Is(err, errNoKey) {
		t.Errorf("after delete: %v", err)
	}
}

func TestKeyringFailuresAreExplainedInWords(t *testing.T) {
	for raw, want := range map[string]string{
		`exec: "dbus-launch": executable file not found in $PATH`:                        "no D-Bus session",
		"dial unix /run/user/0/bus: connect: no such file or directory":                  "no D-Bus session",
		"The name org.freedesktop.secrets was not provided by any .service files":        "no Secret Service is running",
		"failed to unlock correct collection '/org/freedesktop/secrets/aliases/default'": "locked",
		"something nobody has seen before":                                               "something nobody has seen before",
	} {
		got := keyringReason(errors.New(raw))
		if !strings.Contains(got, want) {
			t.Errorf("%q explained as %q, want it to say %q", raw, got, want)
		}
	}
}

func TestTheFallbackWarningSaysWhyInWords(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	was := keyringAvailable
	t.Cleanup(func() { keyringAvailable = was })
	keyringAvailable = func() error { return errors.New(`exec: "dbus-launch": executable file not found in $PATH`) }
	_, out, errOut := runKH("init", "--key-file", keyFile, "--transport-key-file", transportFile)
	if !strings.Contains(out+errOut, "no D-Bus session") || strings.Contains(out+errOut, "dbus-launch") && !strings.Contains(out+errOut, "no D-Bus session") {
		t.Errorf("%s%s", out, errOut)
	}
}
