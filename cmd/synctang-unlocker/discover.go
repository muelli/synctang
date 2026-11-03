// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

// findMr1Devices scans every block device in /proc/partitions for a LUKS2
// header carrying an mr-1 token and returns all of them, in /proc/partitions
// order. "unlocker agent" falls back to this when --device is not given,
// which is always the case in the real dracut deployment: the hook script
// (dracut/90unlocker/unlocker-start.sh) has no device name to pass either.
//
// There can be more than one: a second enrolled volume, a USB stick that
// carries a token. Which of them is the one asking for a password is decided
// by chooseMr1Device, not here.
func findMr1Devices() ([]string, error) {
	data, err := os.ReadFile("/proc/partitions")
	if err != nil {
		return nil, fmt.Errorf("enumerating block devices: %w", err)
	}

	candidates := blockDevices(string(data))
	slog.Log(context.Background(), levelTrace, "scanning block devices for an mr-1 token", "candidates", strings.Join(candidates, ","))
	var found []string
	for _, device := range candidates {
		dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
		if err != nil {
			continue // not LUKS, or not readable; both ordinary here
		}
		if len(tokenIDsOfType(string(dump), mrTokenType)) > 0 {
			slog.Debug("found an mr-1 device", "device", device)
			found = append(found, device)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no block device carries an %s token", mrTokenType)
	}
	return found, nil
}

// discoverAskingDevice finds the mr-1 device that is asking for a password:
// the candidates from findMr1Devices, narrowed by the requests pending in
// askDir (see chooseMr1Device). No pending request is not an error here; it
// only matters when there is more than one candidate.
func discoverAskingDevice(askDir string) (string, error) {
	candidates, err := findMr1Devices()
	if err != nil {
		return "", err
	}
	var ids []string
	if reqs, err := pendingCryptsetupRequests(askDir); err == nil {
		for _, r := range reqs {
			ids = append(ids, r.id)
		}
	}
	return chooseMr1Device(candidates, ids, resolvePath)
}

// resolvePath follows symlinks, so /dev/disk/by-uuid/... and /dev/sda3 can be
// compared; a path that cannot be resolved is compared as it is.
func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// askIDDevice is the device path a password request's Id names, or "" when it
// names none. systemd-cryptsetup identifies its requests as
// "cryptsetup:<device path>" `[I]` (from its source, not observed on a
// running system here); other kinds of request use other ids.
func askIDDevice(id string) string {
	_, path, found := strings.Cut(id, ":")
	if !found || !strings.HasPrefix(path, "/") {
		return ""
	}
	return path
}

// chooseMr1Device decides which candidate is the volume that is asking for a
// password, from the Ids of the password requests that are pending.
//
// With a single candidate there is nothing to decide. With several, taking
// the first would unlock (or fail to unlock) the wrong volume whenever an
// extra enrolled volume or a stick carrying a token sorts ahead of the real
// one: the secret recovered for one volume would be handed to another's
// prompt. So one is chosen only if a pending request names it; of several
// that are asked for, the first in candidate order, because one exchange
// unlocks one volume and the next attempt takes the next. When nothing
// singles one out the agent refuses and says why, rather than guess.
func chooseMr1Device(candidates, requestIDs []string, resolve func(string) string) (string, error) {
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("no block device carries an %s token", mrTokenType)
	case 1:
		return candidates[0], nil
	}
	asked := make(map[string]bool)
	for _, id := range requestIDs {
		if path := askIDDevice(id); path != "" {
			asked[resolve(path)] = true
		}
	}
	for _, c := range candidates {
		if asked[resolve(c)] {
			return c, nil
		}
	}
	pending := "no password request is pending yet"
	if len(requestIDs) > 0 {
		pending = "the pending password request(s) are for " + strings.Join(requestIDs, ", ")
	}
	return "", fmt.Errorf("several block devices carry an %s token (%s) and %s, so none can be told apart from the others; pass --device",
		mrTokenType, strings.Join(candidates, ", "), pending)
}
