// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

const procPartitionsSample = `major minor  #blocks  name

 252        0    6291456 vda
 252        1       2048 vda1
 252        2    1048576 vda2
 252        3    5238784 vda3
   7        0      65536 loop0
  11        0    1048575 sr0
`

func TestBlockCandidatesCarryTheirSizeInBytes(t *testing.T) {
	got := blockCandidates(procPartitionsSample)
	var names []string
	for _, c := range got {
		names = append(names, c.Path)
	}
	if want := "/dev/vda /dev/vda1 /dev/vda2 /dev/vda3"; strings.Join(names, " ") != want {
		t.Fatalf("candidates = %v, want %s (loop and optical devices are never an initrd's volume)", names, want)
	}
	last := got[len(got)-1]
	if last.Name != "vda3" || last.Size != 5238784*1024 {
		t.Fatalf("vda3 = %+v, want 5238784 KiB in bytes", last)
	}
}

func TestHumanSize(t *testing.T) {
	for size, want := range map[int64]string{
		512:            "512 B",
		2 << 20:        "2.0 MiB",
		500 << 20:      "500 MiB",
		5 << 30:        "5.0 GiB",
		500 << 30:      "500 GiB",
		3 << 40:        "3.0 TiB",
		1536 << 20:     "1.5 GiB",
		5238784 * 1024: "5.0 GiB",
	} {
		if got := humanSize(size); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", size, got, want)
		}
	}
}

func TestLuksHeaderInfoReadsVersionUUIDAndLabel(t *testing.T) {
	info := luksHeaderInfo(sampleDump)
	if info.Version != "2" || info.UUID != "442828ce-5625-4ebb-a914-732071988643" || info.Label != "" {
		t.Fatalf("got %+v, want version 2, the sample's UUID and no label (\"(no label)\" is cryptsetup's word for none)", info)
	}
	labelled := strings.Replace(sampleDump, "(no label)", "backup disk", 1)
	if got := luksHeaderInfo(labelled).Label; got != "backup disk" {
		t.Fatalf("label = %q, want \"backup disk\"", got)
	}
}

func TestMappedNamesAreReadFromSysfs(t *testing.T) {
	sys := t.TempDir()
	for _, dir := range []string{"vda3/holders/dm-0", "dm-0/dm"} {
		if err := os.MkdirAll(filepath.Join(sys, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sys, "dm-0", "dm", "name"), []byte("synctang_test_crypt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mappedNames(sys, "vda3"); len(got) != 1 || got[0] != "synctang_test_crypt" {
		t.Fatalf("mappedNames(vda3) = %v, want [synctang_test_crypt]", got)
	}
	if got := mappedNames(sys, "vda2"); len(got) != 0 {
		t.Fatalf("mappedNames(vda2) = %v, want none (it has no holders)", got)
	}
}

func TestDescribeLUKSOnRealImages(t *testing.T) {
	pass := []byte("devices-test-passphrase")
	plain := filepath.Join(t.TempDir(), "plain.img")
	if err := os.WriteFile(plain, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := formatLoopbackImage(t, pass)
	enrolled := formatLoopbackImage(t, pass)
	for i := 0; i < 2; i++ {
		s, err := mrcore.P256().RandomScalar()
		if err != nil {
			t.Fatal(err)
		}
		S, err := mrcore.P256().ScalarBaseMult(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := enrolRecipient(enrolled, pass, S, "box", "MACHINE-ID", ""); err != nil {
			t.Fatalf("enrol %d: %v", i, err)
		}
	}

	if _, ok := describeLUKS(candidate{Name: "plain.img", Path: plain, Size: 1 << 20}, t.TempDir()); ok {
		t.Error("a file that is not a LUKS volume was described as one")
	}

	d, ok := describeLUKS(candidate{Name: "fresh.img", Path: fresh, Size: 32 << 20}, t.TempDir())
	if !ok || d.Version != "2" || d.UUID == "" || d.HasToken || d.KeyHolders != 0 {
		t.Errorf("a fresh LUKS2 image: ok=%v %+v, want LUKS2 with a UUID and no token", ok, d)
	}
	if d.Size != 32<<20 || d.Path != fresh {
		t.Errorf("the candidate's path and size were not carried: %+v", d)
	}

	d, ok = describeLUKS(candidate{Name: "enrolled.img", Path: enrolled, Size: 32 << 20}, t.TempDir())
	if !ok || !d.HasToken || d.KeyHolders != 2 {
		t.Errorf("an image with two key holders: ok=%v %+v, want a token and 2 key holders", ok, d)
	}
}

func TestFormatDevicesAlignsTheColumns(t *testing.T) {
	devs := []luksDevice{
		{Path: "/dev/vda3", UUID: "91ae9308-22e5-4eba-af83-cc26c3c87818", Size: 5 << 30, OpenAs: []string{"synctang_test_crypt"}, HasToken: true, KeyHolders: 2},
		{Path: "/dev/sdb1", UUID: "442828ce-5625-4ebb-a914-732071988643", Size: 500 << 30},
	}
	want := `DEVICE     UUID                                  SIZE     OPEN AS              SYNCTANG
/dev/vda3  91ae9308-22e5-4eba-af83-cc26c3c87818  5.0 GiB  synctang_test_crypt  2 key holders
/dev/sdb1  442828ce-5625-4ebb-a914-732071988643  500 GiB  -                    no token
`
	if got := formatDevices(devs); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWhatSynctangSaysAboutADevice(t *testing.T) {
	for _, tc := range []struct {
		d    luksDevice
		want string
	}{
		{luksDevice{Version: "2"}, "no token"},
		{luksDevice{Version: "2", HasToken: true}, "token, no key holders"},
		{luksDevice{Version: "2", HasToken: true, KeyHolders: 1}, "1 key holder"},
		{luksDevice{Version: "2", HasToken: true, KeyHolders: 3}, "3 key holders"},
		{luksDevice{Version: "1"}, "LUKS1, not supported"},
	} {
		if got := tc.d.synctang(); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.d, got, tc.want)
		}
	}
}

// withDevices points the command at images instead of /proc/partitions, as root or not.
func withDevices(t *testing.T, root bool, paths ...string) {
	t.Helper()
	oldSource, oldRoot, oldSys := devicesSource, isRoot, sysClassBlock
	t.Cleanup(func() { devicesSource, isRoot, sysClassBlock = oldSource, oldRoot, oldSys })
	devicesSource = func() ([]candidate, error) {
		var cs []candidate
		for _, p := range paths {
			cs = append(cs, candidate{Name: filepath.Base(p), Path: p, Size: 32 << 20})
		}
		return cs, nil
	}
	isRoot = func() bool { return root }
	sysClassBlock = t.TempDir()
}

func TestDevicesCommandListsLUKSVolumesAndSkipsTheRest(t *testing.T) {
	pass := []byte("devices-test-passphrase")
	volume := formatLoopbackImage(t, pass)
	plain := filepath.Join(t.TempDir(), "plain.img")
	if err := os.WriteFile(plain, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	withDevices(t, true, plain, volume)

	var out, errOut bytes.Buffer
	if code := run([]string{"devices"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), volume) || !strings.Contains(out.String(), "no token") {
		t.Errorf("the LUKS volume is missing from:\n%s", out.String())
	}
	if strings.Contains(out.String(), plain) {
		t.Errorf("a file that is not LUKS is listed:\n%s", out.String())
	}
	if !strings.HasPrefix(out.String(), "DEVICE") {
		t.Errorf("no header line:\n%s", out.String())
	}
}

func TestDevicesCommandSaysSoWhenThereIsNoLUKSVolume(t *testing.T) {
	cryptsetupOrSkip(t)
	plain := filepath.Join(t.TempDir(), "plain.img")
	if err := os.WriteFile(plain, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	withDevices(t, true, plain)
	var out, errOut bytes.Buffer
	if code := run([]string{"devices"}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "no LUKS device") || out.Len() != 0 {
		t.Errorf("stdout %q stderr %q", out.String(), errOut.String())
	}
}

func TestDevicesCommandNeedsRootToReadTheHeaders(t *testing.T) {
	withDevices(t, false)
	var out, errOut bytes.Buffer
	if code := run([]string{"devices"}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "root") {
		t.Errorf("stderr %q does not say why", errOut.String())
	}
}

func TestDevicesCommandRefusesArguments(t *testing.T) {
	withDevices(t, true)
	var out, errOut bytes.Buffer
	if code := run([]string{"devices", "/dev/sda"}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// Where a person gets stuck is the command that wants a --device: the error names the way
// out, and when it can it lists the volumes.

func TestEnrolWithoutADeviceNamesTheDevicesCommandAndListsTheVolumes(t *testing.T) {
	volume := formatLoopbackImage(t, []byte("devices-test-passphrase"))
	withDevices(t, true, volume)
	var out, errOut bytes.Buffer
	code := run([]string{"enrol", "--pubkey", "04" + strings.Repeat("ab", 64), "--existing-passphrase-file", "/nonexistent"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut.String())
	}
	for _, want := range []string{"--device", "synctang-unlocker devices", volume} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut.String())
		}
	}
}

func TestPairWithoutADeviceNamesTheDevicesCommand(t *testing.T) {
	withDevices(t, false)
	var out, errOut bytes.Buffer
	code := run([]string{"pair", "--existing-passphrase-file", "/nonexistent"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "synctang-unlocker devices") {
		t.Errorf("stderr lacks the way out:\n%s", errOut.String())
	}
}
