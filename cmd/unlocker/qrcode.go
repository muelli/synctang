// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/boombuler/barcode/qr"
)

// quietZone is the light margin a scanner needs to find the symbol's
// edges. Four modules is what the QR specification asks for; without
// it many scanners simply never see the code.
const quietZone = 4

// renderQRCode draws content as a QR code made of Unicode half blocks,
// two module rows per line of text.
//
// Rendered in process rather than by shelling out to qrencode, which is
// what syncthing-socket's setup script does and which makes the tool
// refuse to run on a machine that does not have it. The encoder is
// already linked into this binary anyway: it arrives with
// syncthing-socket, so using it costs nothing.
//
// Both colours are set explicitly on every cell instead of relying on
// the terminal's own foreground and background. A QR code has to be
// dark-on-light to scan, and a terminal with a dark theme would
// otherwise render it inverted, which looks perfectly fine to a human
// and is unreadable to a scanner.
func renderQRCode(content string) (string, error) {
	code, err := qr.Encode(content, qr.M, qr.Auto)
	if err != nil {
		return "", fmt.Errorf("unlocker: encoding the pairing QR code: %w", err)
	}

	bounds := code.Bounds()
	width := bounds.Dx() + 2*quietZone
	height := bounds.Dy() + 2*quietZone

	dark := func(x, y int) bool {
		x -= quietZone
		y -= quietZone
		if x < 0 || y < 0 || x >= bounds.Dx() || y >= bounds.Dy() {
			return false // quiet zone
		}
		r, g, b, _ := code.At(x+bounds.Min.X, y+bounds.Min.Y).RGBA()
		return color.Gray{Y: uint8((r + g + b) / 3 >> 8)}.Y < 128
	}

	// One escape per cell carrying both colours, in the basic ANSI
	// set rather than the 256-colour one: every terminal implements
	// these, and each is eight bytes instead of nineteen. On a symbol
	// with a couple of thousand cells that difference is the
	// difference between a quick redraw and a visibly scrolling one.
	const (
		darkOnDark   = "\x1b[30;40m"
		darkOnLight  = "\x1b[30;47m"
		lightOnDark  = "\x1b[37;40m"
		lightOnLight = "\x1b[37;47m"
		reset        = "\x1b[0m"
	)

	var b strings.Builder
	// Two module rows per text line: the upper half block's foreground
	// is the top row and its background is the bottom row.
	//
	// The colour codes are emitted only where they change, not per
	// cell. A QR code is mostly runs of one colour, and repeating the
	// escapes for every module turned a symbol into tens of kilobytes,
	// which on the 9600-baud serial console this is partly here to
	// serve is most of a minute of scrolling.
	for y := 0; y < height; y += 2 {
		previous := ""
		for x := 0; x < width; x++ {
			top := dark(x, y)
			bottom := y+1 < height && dark(x, y+1)

			style := lightOnLight
			switch {
			case top && bottom:
				style = darkOnDark
			case top && !bottom:
				style = darkOnLight
			case !top && bottom:
				style = lightOnDark
			}
			if style != previous {
				b.WriteString(style)
				previous = style
			}
			b.WriteString("▀") // upper half block
		}
		b.WriteString(reset + "\n")
	}
	return b.String(), nil
}

// renderQRCodePlain draws the same symbol as ASCII, for a terminal that
// cannot show half blocks or colour at all, and for tests that want to
// look at the shape rather than the styling.
func renderQRCodePlain(content string) (string, error) {
	code, err := qr.Encode(content, qr.M, qr.Auto)
	if err != nil {
		return "", fmt.Errorf("unlocker: encoding the pairing QR code: %w", err)
	}

	bounds := code.Bounds()
	var b strings.Builder
	for y := -quietZone; y < bounds.Dy()+quietZone; y++ {
		for x := -quietZone; x < bounds.Dx()+quietZone; x++ {
			inside := x >= 0 && y >= 0 && x < bounds.Dx() && y < bounds.Dy()
			isDark := false
			if inside {
				r, g, bl, _ := code.At(x+bounds.Min.X, y+bounds.Min.Y).RGBA()
				isDark = (r+g+bl)/3>>8 < 128
			}
			if isDark {
				b.WriteString("##")
			} else {
				b.WriteString("  ")
			}
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
