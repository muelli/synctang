// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	neturl "net/url"
	"strings"
	"testing"
	"time"
)

// A QR code that cannot be scanned leaves a person with nothing, so the
// properties worth pinning are the ones that make it scannable at all:
// a quiet zone around it, and a rectangular block of uniform width.
func TestRenderQRCodeIsScannable(t *testing.T) {
	out, err := renderQRCode("synctang://pair?machine=ABCDEFG-HIJKLMN&psk=ORSXG5BRGIZQ")
	if err != nil {
		t.Fatalf("renderQRCode: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 10 {
		t.Fatalf("only %d lines of output, too few to be a QR code", len(lines))
	}

	width := len([]rune(stripANSI(lines[0])))
	for i, line := range lines {
		if got := len([]rune(stripANSI(line))); got != width {
			t.Fatalf("line %d is %d modules wide, want %d: a ragged block will not scan", i, got, width)
		}
	}

	// The quiet zone is not decoration: a scanner needs the light
	// margin to find the symbol's edges at all. Every cell on the first
	// line must therefore be light, which shows up as the absence of
	// the dark colour rather than in the stripped text (every cell
	// renders as the same half-block character whatever its colour).
	for _, darkCode := range []string{"\x1b[30;40m", "\x1b[37;40m", "\x1b[30;47m"} {
		if strings.Contains(lines[0], darkCode) {
			t.Error("the first line has dark modules in it, so there is no quiet zone above the symbol")
			break
		}
	}
}

func TestRenderQRCodeDiffersWithContent(t *testing.T) {
	first, err := renderQRCode("synctang://pair?machine=A&psk=B")
	if err != nil {
		t.Fatalf("renderQRCode: %v", err)
	}
	second, err := renderQRCode("synctang://pair?machine=A&psk=C")
	if err != nil {
		t.Fatalf("renderQRCode: %v", err)
	}
	if first == second {
		t.Fatal("two different payloads rendered identically")
	}
	if first != mustRender(t, "synctang://pair?machine=A&psk=B") {
		t.Fatal("the same payload rendered differently twice")
	}
}

func mustRender(t *testing.T, content string) string {
	t.Helper()
	out, err := renderQRCode(content)
	if err != nil {
		t.Fatalf("renderQRCode: %v", err)
	}
	return out
}

// stripANSI removes colour escapes so the tests can measure the shape
// rather than the styling.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++ // the 'm' itself
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// The pairing payload has to be exactly what the Android app already
// parses, with the one-time code added, or a scan silently produces a
// machine entry that cannot pair.
func TestPairingURLCarriesWhatTheAppNeeds(t *testing.T) {
	url := pairingURL("MACHINE-ID", "study workstation", "ORSXG5BRGIZQ")

	if !strings.HasPrefix(url, "synctang://pair?") {
		t.Fatalf("pairing URL %q does not use the scheme the app parses", url)
	}
	for _, want := range []string{"machine=MACHINE-ID", "psk=ORSXG5BRGIZQ", "name=study+workstation"} {
		if !strings.Contains(url, want) {
			t.Errorf("pairing URL %q is missing %q", url, want)
		}
	}
}

// Whatever cannot be scanned has to be usable by hand, so the printed
// instructions must contain both values on their own.
func TestPairingInstructionsStandAloneWithoutTheQRCode(t *testing.T) {
	text := pairingInstructions("MACHINE-ID", "study workstation", "ORSXG5BRGIZQ", 5*time.Minute)

	for _, want := range []string{"MACHINE-ID", "ORSXG5BRGIZQ", "5m0s"} {
		if !strings.Contains(text, want) {
			t.Errorf("instructions do not mention %q:\n%s", want, text)
		}
	}
}

// The Android side decodes this payload with java.net.URLDecoder, which
// treats "+" as a space, and normalises the Device ID by keeping only
// base32 characters. Both sides have to agree about that or a scan
// produces a machine entry that cannot be dialled, which is the kind of
// mismatch that only shows up on a phone.
func TestPairingURLSurvivesTheAppsDecoding(t *testing.T) {
	const (
		machineID = "ABCDEFG-HIJKLMN-OPQRSTU-VWXYZ23-456ABCD-EFGHIJK-LMNOPQR-STUVWXY"
		code      = "ORSXG5BRGIZQABCDEFGHIJKLMN"
	)
	raw := pairingURL(machineID, "study workstation", code)

	query, err := neturl.ParseQuery(strings.TrimPrefix(raw, "synctang://pair?"))
	if err != nil {
		t.Fatalf("the payload is not a parseable query: %v", err)
	}
	if got := query.Get("machine"); got != machineID {
		t.Errorf("machine = %q, want %q", got, machineID)
	}
	if got := query.Get("name"); got != "study workstation" {
		t.Errorf("name = %q, want the space back, not a plus", got)
	}
	// The pairing code must need no escaping at all: base32 is
	// alphanumeric, so anything escaped here would mean the encoding
	// changed and the app would decode a different secret.
	if got := query.Get("psk"); got != code {
		t.Errorf("psk = %q, want %q", got, code)
	}
	if !strings.Contains(raw, "psk="+code) {
		t.Errorf("the pairing code was escaped in %q; it must travel literally", raw)
	}
}
