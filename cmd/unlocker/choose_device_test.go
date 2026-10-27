// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// fakeResolve stands in for filepath.EvalSymlinks: /dev/disk/by-uuid/*
// names are symlinks to the real node, anything else resolves to itself.
func fakeResolve(links map[string]string) func(string) string {
	return func(p string) string {
		if target, ok := links[p]; ok {
			return target
		}
		return p
	}
}

// With a single candidate there is nothing to choose, and the choice must
// not start depending on whether a password request is already pending.
func TestChooseMr1DeviceWithOneCandidateUsesIt(t *testing.T) {
	got, err := chooseMr1Device([]string{"/dev/sda3"}, nil, fakeResolve(nil))
	if err != nil || got != "/dev/sda3" {
		t.Fatalf("got (%q, %v), want /dev/sda3", got, err)
	}
	got, err = chooseMr1Device([]string{"/dev/sda3"}, []string{"cryptsetup:/dev/sdb1"}, fakeResolve(nil))
	if err != nil || got != "/dev/sda3" {
		t.Fatalf("a request for another device changed the choice: (%q, %v)", got, err)
	}
}

func TestChooseMr1DeviceWithNoCandidateFails(t *testing.T) {
	if _, err := chooseMr1Device(nil, nil, fakeResolve(nil)); err == nil {
		t.Fatal("no candidate did not fail")
	}
}

// The point of the change: a USB stick or a second enrolled volume that
// sorts first must not be taken for the volume that is actually asking.
func TestChooseMr1DeviceFollowsThePendingRequest(t *testing.T) {
	links := map[string]string{"/dev/disk/by-uuid/AAAA-1111": "/dev/sdb2"}
	got, err := chooseMr1Device(
		[]string{"/dev/sda1", "/dev/sdb2"},
		[]string{"cryptsetup:/dev/disk/by-uuid/AAAA-1111"},
		fakeResolve(links))
	if err != nil || got != "/dev/sdb2" {
		t.Fatalf("got (%q, %v), want /dev/sdb2, the device the request is for", got, err)
	}
}

func TestChooseMr1DeviceRefusesWhenNothingIsPending(t *testing.T) {
	_, err := chooseMr1Device([]string{"/dev/sda1", "/dev/sdb2"}, nil, fakeResolve(nil))
	if err == nil {
		t.Fatal("two candidates and no request was accepted")
	}
	for _, want := range []string{"/dev/sda1", "/dev/sdb2", "--device"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error does not mention %q: %v", want, err)
		}
	}
}

func TestChooseMr1DeviceRefusesWhenTheRequestMatchesNone(t *testing.T) {
	_, err := chooseMr1Device([]string{"/dev/sda1", "/dev/sdb2"}, []string{"cryptsetup:/dev/sdc9"}, fakeResolve(nil))
	if err == nil {
		t.Fatal("a request for neither candidate was accepted")
	}
	if !strings.Contains(err.Error(), "/dev/sdc9") {
		t.Fatalf("the error does not say what was being asked for: %v", err)
	}
}

// Two enrolled volumes both asking: one exchange unlocks one volume, so the
// first candidate (in /proc/partitions order) that is being asked for wins,
// deterministically, and the other is dealt with by the next attempt.
func TestChooseMr1DevicePicksTheFirstOfSeveralPendingVolumes(t *testing.T) {
	got, err := chooseMr1Device(
		[]string{"/dev/sda1", "/dev/sdb2"},
		[]string{"cryptsetup:/dev/sdb2", "cryptsetup:/dev/sda1"},
		fakeResolve(nil))
	if err != nil || got != "/dev/sda1" {
		t.Fatalf("got (%q, %v), want the first candidate that is asked for", got, err)
	}
}

// An Id that is not a device path (systemd uses other ids for other kinds of
// request) says nothing about which device is meant.
func TestChooseMr1DeviceIgnoresIdsThatAreNotPaths(t *testing.T) {
	_, err := chooseMr1Device([]string{"/dev/sda1", "/dev/sdb2"}, []string{"cryptsetup:root", "cryptsetup"}, fakeResolve(nil))
	if err == nil {
		t.Fatal("an id that names no device singled one out")
	}
}

// Answering must go to the request that belongs to the device that was
// unlocked, not merely to the first one pending.
func TestSelectAskRequestPrefersTheOneForTheDevice(t *testing.T) {
	reqs := []askRequest{
		{socket: "/run/ask/first", id: "cryptsetup:/dev/sda1"},
		{socket: "/run/ask/second", id: "cryptsetup:/dev/sdb2"},
	}
	got, ok := selectAskRequest(reqs, "/dev/sdb2", fakeResolve(nil))
	if !ok || got.socket != "/run/ask/second" {
		t.Fatalf("got (%+v, %v), want the request for /dev/sdb2", got, ok)
	}
}

func TestSelectAskRequestFallsBackToTheFirst(t *testing.T) {
	reqs := []askRequest{
		{socket: "/run/ask/first", id: "cryptsetup:/dev/sdc1"},
		{socket: "/run/ask/second", id: "cryptsetup:/dev/sdd1"},
	}
	got, ok := selectAskRequest(reqs, "/dev/sdb2", fakeResolve(nil))
	if !ok || got.socket != "/run/ask/first" {
		t.Fatalf("got (%+v, %v), want the first request when none names the device", got, ok)
	}
	if _, ok := selectAskRequest(nil, "/dev/sdb2", fakeResolve(nil)); ok {
		t.Fatal("no requests yielded a request")
	}
}
