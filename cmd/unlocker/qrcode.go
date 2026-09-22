// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"

	"github.com/mdp/qrterminal/v3"
)

// quietZone is the light margin a scanner needs to find the symbol's
// edges. Four modules is what the QR specification asks for; without
// it many scanners simply never see the code.
const quietZone = qrterminal.QUIET_ZONE

// Colour-explicit half-block cells.
//
// qrterminal's own half-block constants ("█" for a light module, " "
// for a dark one) take the terminal's foreground and background as
// light and dark respectively, which is right on a dark theme and
// exactly inverted on a light one. A human reads either happily; a
// scanner needs dark-on-light and will not read the inverted one. So
// both colours are stated on every cell rather than inherited.
//
// The basic ANSI colours are used rather than the 256-colour ones:
// every terminal implements them, and they are half the bytes, which
// matters on the serial console this is partly here to serve.
const (
	// "▀" draws its upper half in the foreground colour and its lower
	// half in the background colour, so one cell carries two module
	// rows.
	cellDarkOverLight  = "\x1b[30;47m▀\x1b[0m"
	cellLightOverDark  = "\x1b[37;40m▀\x1b[0m"
	cellDarkOverDark   = "\x1b[40m \x1b[0m"
	cellLightOverLight = "\x1b[47m \x1b[0m"
)

// renderQRCode draws content as a QR code of Unicode half blocks, two
// module rows per line of text.
//
// The layout comes from qrterminal, which syncthing-socket uses for the
// same job and which is already linked into this binary (it arrives
// with syncthing-socket's own package), so this is neither a new
// dependency nor hand-rolled geometry. Only the cell strings are ours,
// for the contrast reason above.
//
// Drawn in process rather than by shelling out to qrencode, which is
// what syncthing-socket's initramfs setup script does: that makes the
// tool refuse to run wherever the binary is not installed, and pairing
// happens on machines that were never set up for it.
func renderQRCode(content string) (string, error) {
	var b strings.Builder
	qrterminal.GenerateWithConfig(content, qrterminal.Config{
		Level:          qrterminal.M,
		Writer:         &b,
		QuietZone:      quietZone,
		HalfBlocks:     true,
		BlackChar:      cellDarkOverDark,
		WhiteChar:      cellLightOverLight,
		BlackWhiteChar: cellDarkOverLight,
		WhiteBlackChar: cellLightOverDark,
	})
	return b.String(), nil
}

// renderQRCodePlain draws the same symbol without colour, for a
// terminal that cannot show it and for anything being piped into a file
// or a log, where escape sequences are noise. Full blocks rather than
// half, two characters wide per module, because without colour the only
// thing distinguishing a module is the character itself.
func renderQRCodePlain(content string) (string, error) {
	var b strings.Builder
	qrterminal.GenerateWithConfig(content, qrterminal.Config{
		Level:     qrterminal.M,
		Writer:    &b,
		QuietZone: quietZone,
		BlackChar: "##",
		WhiteChar: "  ",
	})
	return b.String(), nil
}
