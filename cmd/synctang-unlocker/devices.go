// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"github.com/muelli/synctang/internal/gnuflag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
)

// candidate is a block device that might carry a LUKS header: where it is, what the kernel
// calls it, and how big, from /proc/partitions.
type candidate struct {
	Name string
	Path string
	Size int64 // bytes
}

// blockCandidates lists the devices in the text of /proc/partitions that can be a LUKS
// container in an initrd or on a running system: not RAM disks, loop devices, compressed
// RAM or optical drives. See blockDevices.
func blockCandidates(procPartitions string) []candidate {
	var out []candidate
	for _, line := range strings.Split(procPartitions, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 || fields[0] == "major" {
			continue
		}
		name := fields[3]
		if strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "loop") ||
			strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "sr") {
			continue
		}
		blocks, _ := strconv.ParseInt(fields[2], 10, 64) // 1 KiB units
		out = append(out, candidate{Name: name, Path: "/dev/" + name, Size: blocks * 1024})
	}
	return out
}

// Seams for tests: where the candidates come from, whether this is root, and where sysfs is.
var (
	isRoot        = func() bool { return os.Geteuid() == 0 }
	sysClassBlock = "/sys/class/block"
	devicesSource = func() ([]candidate, error) {
		data, err := os.ReadFile("/proc/partitions")
		if err != nil {
			return nil, fmt.Errorf("enumerating block devices: %w", err)
		}
		return blockCandidates(string(data)), nil
	}
)

// luksInfo is what the header of a LUKS volume says about itself.
type luksInfo struct {
	Version, UUID, Label string
}

var luksHeaderLine = regexp.MustCompile(`(?m)^(Version|UUID|Label):[ \t]*(.*?)[ \t]*$`)

// luksHeaderInfo reads the version, UUID and label from the output of "cryptsetup luksDump".
// cryptsetup writes "(no label)" for a volume without one.
func luksHeaderInfo(dump string) luksInfo {
	var info luksInfo
	for _, m := range luksHeaderLine.FindAllStringSubmatch(dump, -1) {
		switch m[1] {
		case "Version":
			if info.Version == "" {
				info.Version = m[2]
			}
		case "UUID":
			if info.UUID == "" {
				info.UUID = m[2]
			}
		case "Label":
			if info.Label == "" && m[2] != "(no label)" {
				info.Label = m[2]
			}
		}
	}
	return info
}

// humanSize is a size in bytes in the largest binary unit that keeps it at one or more,
// with one decimal below 100 and none above ("5.0 GiB", "500 GiB").
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	size, suffix := float64(n), ""
	for _, s := range []string{"KiB", "MiB", "GiB", "TiB", "PiB"} {
		size /= unit
		suffix = s
		if size < unit {
			break
		}
	}
	if size >= 100 {
		return fmt.Sprintf("%.0f %s", size, suffix)
	}
	return fmt.Sprintf("%.1f %s", size, suffix)
}

// mappedNames are the names a device is open under, from sysfs: each holder of the device
// (dm-0, say) that is a device-mapper device has a name there.
func mappedNames(sys, name string) []string {
	holders, err := os.ReadDir(filepath.Join(sys, name, "holders"))
	if err != nil {
		return nil
	}
	var names []string
	for _, h := range holders {
		raw, err := os.ReadFile(filepath.Join(sys, h.Name(), "dm", "name"))
		if err != nil {
			continue
		}
		if n := strings.TrimSpace(string(raw)); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// luksDevice is one LUKS volume and what synctang knows about it.
type luksDevice struct {
	Path       string
	Size       int64
	Version    string
	UUID       string
	Label      string
	OpenAs     []string
	HasToken   bool
	KeyHolders int
}

// synctang says what is enrolled on the volume, in words.
func (d luksDevice) synctang() string {
	switch {
	case d.Version == "1":
		return "LUKS1, not supported"
	case !d.HasToken:
		return "no token"
	case d.KeyHolders == 0:
		return "token, no key holders"
	case d.KeyHolders == 1:
		return "1 key holder"
	}
	return fmt.Sprintf("%d key holders", d.KeyHolders)
}

// describeLUKS reads c's LUKS header and, if there is one, says what it holds. False when c
// is not a LUKS volume or cannot be read, both ordinary among a machine's block devices.
func describeLUKS(c candidate, sys string) (luksDevice, bool) {
	dump, err := runCommand(nil, "cryptsetup", "luksDump", c.Path)
	if err != nil {
		return luksDevice{}, false
	}
	info := luksHeaderInfo(string(dump))
	d := luksDevice{
		Path: c.Path, Size: c.Size,
		Version: info.Version, UUID: info.UUID, Label: info.Label,
		OpenAs: mappedNames(sys, c.Name),
	}
	if tok, id, err := loadToken(string(dump), c.Path, "", ""); err != nil {
		d.HasToken = len(tokenIDsOfType(string(dump), mrTokenType)) > 0
	} else if id >= 0 {
		d.HasToken = true
		d.KeyHolders = len(tok.Recipients)
	}
	return d, true
}

// listDevices describes every LUKS volume among the machine's block devices.
func listDevices() ([]luksDevice, error) {
	candidates, err := devicesSource()
	if err != nil {
		return nil, err
	}
	var found []luksDevice
	for _, c := range candidates {
		if d, ok := describeLUKS(c, sysClassBlock); ok {
			found = append(found, d)
		}
	}
	return found, nil
}

// formatDevices lays the volumes out as a table with a header line.
func formatDevices(devs []luksDevice) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DEVICE\tUUID\tSIZE\tOPEN AS\tSYNCTANG")
	for _, d := range devs {
		open := "-"
		if len(d.OpenAs) > 0 {
			open = strings.Join(d.OpenAs, ",")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", d.Path, d.UUID, humanSize(d.Size), open, d.synctang())
	}
	w.Flush()
	return b.String()
}

// runDevices lists the LUKS volumes on this machine, so that the device to pair or enrol
// does not have to be guessed. It reads headers, which takes root.
func runDevices(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("devices", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "synctang-unlocker: devices takes no arguments, got %q\n", fs.Arg(0))
		return 2
	}
	if !isRoot() {
		fmt.Fprintln(stderr, "synctang-unlocker: devices reads LUKS headers, which takes root; run it as root")
		return 1
	}
	devs, err := listDevices()
	if err != nil {
		fmt.Fprintf(stderr, "synctang-unlocker: %v\n", err)
		return 1
	}
	if len(devs) == 0 {
		fmt.Fprintln(stderr, "synctang-unlocker: no LUKS device found")
		return 1
	}
	fmt.Fprint(stdout, formatDevices(devs))
	return 0
}

// deviceHint is what to tell someone who left out --device: the command that lists the
// volumes, and the volumes themselves when they can be read.
func deviceHint() string {
	hint := `run "synctang-unlocker devices" to list the LUKS devices on this machine`
	if !isRoot() {
		return hint + " (as root)"
	}
	devs, err := listDevices()
	if err != nil || len(devs) == 0 {
		return hint
	}
	var b strings.Builder
	b.WriteString(hint + "; found:")
	for _, d := range devs {
		fmt.Fprintf(&b, "\n  %s  %s, uuid %s, %s", d.Path, humanSize(d.Size), d.UUID, d.synctang())
	}
	return b.String()
}
