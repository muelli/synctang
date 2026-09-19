// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// blockDevices lists candidate whole devices and partitions from the
// text of /proc/partitions, skipping kinds that are never a LUKS
// container in an initrd: this needs no lsblk and no udev, which is
// the point, since systemd-cryptsetup sets no environment variable
// naming the device the way initramfs-tools' askpass could rely on.
func blockDevices(procPartitions string) []string {
	var devices []string
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
		devices = append(devices, "/dev/"+name)
	}
	return devices
}

// findMr1Device scans every block device in /proc/partitions for a
// LUKS2 header carrying an mr-1 token, returning the first match. It
// is what "unlocker agent" falls back to when --device is not given,
// which is always the case in the real dracut deployment: the hook
// script (dracut/90unlocker/unlocker-start.sh) has no device name to
// pass either.
func findMr1Device() (string, error) {
	data, err := os.ReadFile("/proc/partitions")
	if err != nil {
		return "", fmt.Errorf("enumerating block devices: %w", err)
	}

	candidates := blockDevices(string(data))
	slog.Log(context.Background(), levelTrace, "scanning block devices for an mr-1 token", "candidates", strings.Join(candidates, ","))
	for _, device := range candidates {
		dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
		if err != nil {
			continue // not LUKS, or not readable; both ordinary here
		}
		if len(tokenIDsOfType(string(dump), mrTokenType)) > 0 {
			slog.Debug("found the mr-1 device", "device", device)
			return device, nil
		}
	}

	return "", fmt.Errorf("no block device carries an %s token", mrTokenType)
}
