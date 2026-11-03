// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/syncthing/syncthing/lib/protocol"
)

// Valid Syncthing Device IDs for tests that enrol recipients. Enrolment
// refuses a transport id that is not a real Device ID, because one that is
// mistyped can never match what the transport reports, so placeholder
// strings no longer do. Each is the Device ID of the certificate bytes
// named in the comment, so the values are not arbitrary.
const (
	testKH1         = "LWJQ3KS-5FWYWAL-WRA2LYN-UXWBZK6-SRCTIMX-R3E5BPX-A6K76KZ-YH7VUQK" // "KH-1"
	testKH2         = "DW3KYPQ-ZFMZL4A-BEFRKST-RWUCKMJ-EHKDG6C-WO4A2W2-SFRKFMT-IC3SVAJ" // "KH-2"
	testKeyholderID = "C2LUOQL-J6FTVSR-FCRB737-GRGR3SM-N5PONWC-RVEEDDE-7ADSPY2-AAJN3QZ" // "KEYHOLDER-DEVICE-ID"
	testCLIRecipID  = "CCPHDID-YYQBOMU-POYEOR5-2QVHEK6-PWQCFLF-MPAAKQF-ELN2ZGG-TUWNMAM" // "CLI-TEST-RECIPIENT-ID"
)

func TestTestDeviceIDsAreWhatTheyClaimToBe(t *testing.T) {
	for source, id := range map[string]string{
		"KH-1":                  testKH1,
		"KH-2":                  testKH2,
		"KEYHOLDER-DEVICE-ID":   testKeyholderID,
		"CLI-TEST-RECIPIENT-ID": testCLIRecipID,
	} {
		if want := protocol.NewDeviceID([]byte(source)).String(); id != want {
			t.Errorf("constant for %q is %s, want %s", source, id, want)
		}
		if got, err := canonicalDeviceID(id); err != nil || got != id {
			t.Errorf("%s is not canonical: (%q, %v)", id, got, err)
		}
	}
}
