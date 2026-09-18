// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

const sampleProcPartitions = `major minor  #blocks  name

   7        0     131072 loop0
 253        0   16777216 vda
 253        1     524288 vda1
 253        2   16250880 vda2
   1        0       4096 ram0
`

func TestBlockDevices(t *testing.T) {
	got := blockDevices(sampleProcPartitions)
	want := []string{"/dev/vda", "/dev/vda1", "/dev/vda2"}
	if len(got) != len(want) {
		t.Fatalf("blockDevices: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("blockDevices: got %v want %v", got, want)
		}
	}
}

func TestBlockDevicesSkipsHeaderAndBlankLines(t *testing.T) {
	got := blockDevices("major minor  #blocks  name\n\n")
	if len(got) != 0 {
		t.Fatalf("expected no devices from a header-only input, got %v", got)
	}
}
