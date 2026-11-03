// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"rsc.io/qr"
)

const imageTestContent = "synctang://pair?machine=H5APP3N-YIQZANN-4LPEVJF-K56O4RF-JH3AWOJ-THBBOI6-BHKBC7J-QGRKIAX&name=synctang-local&psk=KMAXCD654UPFOAB2ICWOPBDUUQ"

// modulesOfPNG reads a PNG back into a QR module grid by sampling the middle
// of every module's cell, so the test checks the picture and not the code
// that drew it.
func modulesOfPNG(t *testing.T, data []byte, scale int) (grid [][]bool, quiet int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() || b.Dx()%scale != 0 {
		t.Fatalf("image is %dx%d, not a square of %d-pixel modules", b.Dx(), b.Dy(), scale)
	}
	n := b.Dx() / scale
	grid = make([][]bool, n)
	for y := 0; y < n; y++ {
		grid[y] = make([]bool, n)
		for x := 0; x < n; x++ {
			r, g, bl, _ := img.At(b.Min.X+x*scale+scale/2, b.Min.Y+y*scale+scale/2).RGBA()
			grid[y][x] = r < 0x8000 && g < 0x8000 && bl < 0x8000 // dark
		}
	}
	return grid, 0
}

// The picture has to be the same QR code the block rendering draws, with the
// light margin a scanner needs around it, or a phone will not read it.
func TestTheImageIsTheQRCodeWithItsQuietZone(t *testing.T) {
	code, err := qr.Encode(imageTestContent, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	const scale = 8
	data, err := qrPNG(imageTestContent, scale)
	if err != nil {
		t.Fatal(err)
	}
	grid, _ := modulesOfPNG(t, data, scale)

	if want := code.Size + 2*quietZone; len(grid) != want {
		t.Fatalf("image is %d modules wide, want %d (code %d plus a %d-module margin each side)", len(grid), want, code.Size, quietZone)
	}
	for y := range grid {
		for x := range grid[y] {
			inCode := x >= quietZone && x < quietZone+code.Size && y >= quietZone && y < quietZone+code.Size
			want := inCode && code.Black(x-quietZone, y-quietZone)
			if grid[y][x] != want {
				t.Fatalf("module (%d,%d) is dark=%v, want %v", x, y, grid[y][x], want)
			}
		}
	}
}

func decodeChunks(t *testing.T, payloads []string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.Join(payloads, ""))
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	return raw
}

// The Kitty graphics protocol sends an image as APC sequences with at most 4096
// bytes of base64 each, "m=1" on every chunk but the last.
func TestKittyCarriesThePNGInOrderedChunks(t *testing.T) {
	out, err := renderQRKitty(imageTestContent, false)
	if err != nil {
		t.Fatal(err)
	}
	apc := regexp.MustCompile("\x1b_G([^;\x1b]*);([^\x1b]*)\x1b\\\\")
	parts := apc.FindAllStringSubmatch(out, -1)
	if len(parts) == 0 {
		t.Fatalf("no kitty graphics sequence in %q", out)
	}
	if !strings.Contains(parts[0][1], "a=T") || !strings.Contains(parts[0][1], "f=100") {
		t.Errorf("the first chunk must say what to do (a=T) and that it is PNG (f=100): %q", parts[0][1])
	}
	var payloads []string
	for i, p := range parts {
		if len(p[2]) > 4096 {
			t.Errorf("chunk %d carries %d bytes, over the 4096 limit", i, len(p[2]))
		}
		more := strings.Contains(p[1], "m=1")
		if i < len(parts)-1 && !more {
			t.Errorf("chunk %d is not marked m=1 although more follow: %q", i, p[1])
		}
		if i == len(parts)-1 && !strings.Contains(p[1], "m=0") {
			t.Errorf("the last chunk must say m=0: %q", p[1])
		}
		payloads = append(payloads, p[2])
	}
	want, _ := qrPNG(imageTestContent, qrImageScale)
	if got := decodeChunks(t, payloads); !bytes.Equal(got, want) {
		t.Error("the chunks do not reassemble into the QR code's PNG")
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("the cursor must end on a fresh line below the image")
	}
}

func TestITerm2CarriesThePNGWithItsSize(t *testing.T) {
	out, err := renderQRITerm2(imageTestContent, false)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("\x1b\\]1337;File=([^:]*):([^\a\x1b]*)(\a|\x1b\\\\)").FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no iTerm2 inline image in %q", out)
	}
	if !strings.Contains(m[1], "inline=1") {
		t.Errorf("the image must be inline: %q", m[1])
	}
	want, _ := qrPNG(imageTestContent, qrImageScale)
	if !strings.Contains(m[1], "size="+strconv.Itoa(len(want))) {
		t.Errorf("size= must be the PNG's %d bytes: %q", len(want), m[1])
	}
	if got := decodeChunks(t, []string{m[2]}); !bytes.Equal(got, want) {
		t.Error("the payload is not the QR code's PNG")
	}
}

// A multiplexer swallows graphics sequences unless they are wrapped in its
// passthrough, with every ESC inside doubled.
func TestTmuxPassthroughDoublesEveryEscape(t *testing.T) {
	got := wrapForTmux("\x1b_Ga=T;AAAA\x1b\\")
	want := "\x1bPtmux;\x1b\x1b_Ga=T;AAAA\x1b\x1b\\\x1b\\"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	out, _ := renderQRKitty(imageTestContent, true)
	if !strings.HasPrefix(out, "\x1bPtmux;") {
		t.Errorf("a tmux rendering must start with the passthrough: %.20q", out)
	}
}

func TestWhichTerminalsGetAnImage(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want imageProtocol
	}{
		{"kitty by window id", map[string]string{"KITTY_WINDOW_ID": "3"}, protoKitty},
		{"kitty by TERM", map[string]string{"TERM": "xterm-kitty"}, protoKitty},
		{"ghostty by TERM", map[string]string{"TERM": "xterm-ghostty"}, protoKitty},
		{"ghostty by program", map[string]string{"TERM_PROGRAM": "ghostty"}, protoKitty},
		{"iTerm2 by program", map[string]string{"TERM_PROGRAM": "iTerm.app"}, protoITerm2},
		{"iTerm2 over ssh", map[string]string{"LC_TERMINAL": "iTerm2"}, protoITerm2},
		{"WezTerm", map[string]string{"TERM_PROGRAM": "WezTerm"}, protoITerm2},
		{"plain xterm", map[string]string{"TERM": "xterm-256color"}, noImage},
		{"linux console", map[string]string{"TERM": "linux"}, noImage},
		{"nothing set", map[string]string{}, noImage},
		// Inside a multiplexer the answer is no unless asked for explicitly.
		{"kitty under tmux", map[string]string{"KITTY_WINDOW_ID": "3", "TMUX": "/tmp/tmux-1000/default,1,0"}, noImage},
		{"iTerm2 under screen", map[string]string{"TERM_PROGRAM": "iTerm.app", "STY": "123.pts-0.host"}, noImage},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectImageProtocol(func(k string) string { return c.env[k] }); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func kindOf(out string) string {
	switch {
	case strings.Contains(out, "\x1b_G"):
		return "kitty"
	case strings.Contains(out, "\x1b]1337;File="):
		return "iterm2"
	case strings.Contains(out, "##"):
		return "plain"
	case strings.Contains(out, "▀"):
		return "blocks"
	}
	return "unknown"
}

func TestTheAutomaticChoiceOfRendering(t *testing.T) {
	kitty := map[string]string{"KITTY_WINDOW_ID": "1"}
	cases := []struct {
		name  string
		mode  string
		env   map[string]string
		isTTY bool
		want  string
	}{
		{"auto on a kitty terminal", "auto", kitty, true, "kitty"},
		{"auto, kitty but output is a file", "auto", kitty, false, "blocks"},
		{"auto on an ordinary terminal", "auto", map[string]string{"TERM": "xterm"}, true, "blocks"},
		{"auto honours NO_COLOR", "auto", map[string]string{"KITTY_WINDOW_ID": "1", "NO_COLOR": "1"}, true, "plain"},
		{"auto under tmux", "auto", map[string]string{"KITTY_WINDOW_ID": "1", "TMUX": "x"}, true, "blocks"},
		{"blocks always blocks", "blocks", kitty, true, "blocks"},
		{"plain always plain", "plain", kitty, true, "plain"},
		{"kitty when asked, anywhere", "kitty", map[string]string{}, false, "kitty"},
		{"iterm2 when asked, anywhere", "iterm2", map[string]string{}, false, "iterm2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := renderPairingQR(imageTestContent, c.mode, func(k string) string { return c.env[k] }, c.isTTY)
			if err != nil {
				t.Fatal(err)
			}
			if got := kindOf(out); got != c.want {
				t.Errorf("rendered %s, want %s", got, c.want)
			}
		})
	}
}

func TestAnUnknownRenderingIsRefused(t *testing.T) {
	if _, err := renderPairingQR(imageTestContent, "hologram", func(string) string { return "" }, true); err == nil {
		t.Fatal("expected an error")
	}
	for _, ok := range []string{"auto", "blocks", "plain", "kitty", "iterm2"} {
		if !validQRMode(ok) {
			t.Errorf("%s should be valid", ok)
		}
	}
	if validQRMode("hologram") {
		t.Error("hologram should not be valid")
	}
}

func TestPairRefusesAnUnknownQRMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"pair", "--device", "/dev/null", "--existing-passphrase-file", "/dev/null", "--qr", "hologram"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--qr") {
		t.Errorf("exit %d, stderr %q: want 2 and a message naming --qr", code, stderr.String())
	}
}
