// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"

	"rsc.io/qr"
)

// The QR code as a real picture, for terminals that can show one. Half blocks
// (qrcode.go) work everywhere but are only ever as sharp as the terminal's
// font; an image is drawn by the terminal at its own resolution, which is what
// a phone camera likes.
//
// Two protocols cover most terminals people use for this: the Kitty graphics
// protocol (Kitty, Ghostty) and iTerm2's inline images (iTerm2, WezTerm). Sixel
// is not here: whether a terminal can show it is a question the terminal has to
// be asked, which means reading its reply in raw mode, and this command is also
// run over a serial console.

const (
	// qrImageScale is how many image pixels one QR module gets; the terminal
	// scales the whole picture to qrImageColumns cells wide.
	qrImageScale   = 8
	qrImageColumns = 40
	// kittyChunk is the most base64 the Kitty protocol takes in one escape sequence.
	kittyChunk = 4096
)

type imageProtocol int

const (
	noImage imageProtocol = iota
	protoKitty
	protoITerm2
)

func (p imageProtocol) String() string {
	switch p {
	case protoKitty:
		return "kitty"
	case protoITerm2:
		return "iterm2"
	}
	return "none"
}

// qrModes are the values --qr takes.
var qrModes = []string{"auto", "blocks", "plain", "kitty", "iterm2"}

func validQRMode(mode string) bool {
	for _, m := range qrModes {
		if m == mode {
			return true
		}
	}
	return false
}

// qrPNG draws content as a black-on-white PNG with scale pixels to a module and
// the quiet zone a scanner needs around it. It is the same QR code, at the same
// error correction level, that the block rendering draws.
func qrPNG(content string, scale int) ([]byte, error) {
	code, err := qr.Encode(content, qr.M)
	if err != nil {
		return nil, fmt.Errorf("encoding the QR code: %w", err)
	}
	modules := code.Size + 2*quietZone
	img := image.NewPaletted(image.Rect(0, 0, modules*scale, modules*scale),
		color.Palette{color.White, color.Black})
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if !code.Black(x, y) {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetColorIndex((quietZone+x)*scale+dx, (quietZone+y)*scale+dy, 1)
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// wrapForTmux passes a sequence through tmux to the terminal outside it, which
// otherwise swallows graphics sequences. Every ESC inside has to be doubled.
func wrapForTmux(seq string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// renderQRKitty draws the image with the Kitty graphics protocol: the PNG
// (f=100) sent as base64 in chunks of at most 4096 bytes, "m=1" on every chunk
// but the last. q=2 asks the terminal not to answer, so nothing lands in the
// input of whatever runs next.
func renderQRKitty(content string, tmux bool) (string, error) {
	data, err := qrPNG(content, qrImageScale)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for first := true; len(encoded) > 0; first = false {
		n := min(len(encoded), kittyChunk)
		chunk, rest := encoded[:n], encoded[n:]
		more := 0
		if len(rest) > 0 {
			more = 1
		}
		control := fmt.Sprintf("q=2,m=%d", more)
		if first {
			control = fmt.Sprintf("a=T,f=100,t=d,c=%d,q=2,m=%d", qrImageColumns, more)
		}
		seq := "\x1b_G" + control + ";" + chunk + "\x1b\\"
		if tmux {
			seq = wrapForTmux(seq)
		}
		b.WriteString(seq)
		encoded = rest
	}
	b.WriteString("\n")
	return b.String(), nil
}

// renderQRITerm2 draws the image with iTerm2's inline image protocol, which
// WezTerm and some other terminals understand as well.
func renderQRITerm2(content string, tmux bool) (string, error) {
	data, err := qrPNG(content, qrImageScale)
	if err != nil {
		return "", err
	}
	seq := fmt.Sprintf("\x1b]1337;File=name=%s;size=%d;inline=1;width=%d;preserveAspectRatio=1:%s\a",
		base64.StdEncoding.EncodeToString([]byte("pairing-qr.png")), len(data), qrImageColumns,
		base64.StdEncoding.EncodeToString(data))
	if tmux {
		seq = wrapForTmux(seq)
	}
	return seq + "\n", nil
}

// detectImageProtocol says which image protocol the terminal this runs in is
// known to speak, judging by what it puts in the environment. Inside a
// multiplexer the answer is none: the sequences do not get through unless the
// multiplexer is set up for it, and a wrong guess prints nothing at all, which is
// worse than blocks. --qr kitty or --qr iterm2 is for that case.
func detectImageProtocol(getenv func(string) string) imageProtocol {
	term := getenv("TERM")
	if getenv("TMUX") != "" || getenv("STY") != "" ||
		strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux") {
		return noImage
	}
	switch {
	case getenv("KITTY_WINDOW_ID") != "", term == "xterm-kitty", term == "xterm-ghostty",
		getenv("TERM_PROGRAM") == "ghostty":
		return protoKitty
	case getenv("TERM_PROGRAM") == "iTerm.app", getenv("TERM_PROGRAM") == "WezTerm",
		getenv("LC_TERMINAL") == "iTerm2":
		return protoITerm2
	}
	return noImage
}

// renderPairingQR draws the pairing QR code as --qr asks. "auto", the default,
// is an image where the output is a terminal that is known to show one, the
// plain ASCII code under NO_COLOR, and half blocks otherwise.
func renderPairingQR(content, mode string, getenv func(string) string, isTerminal bool) (string, error) {
	tmux := getenv("TMUX") != ""
	switch mode {
	case "blocks":
		return renderQRCode(content)
	case "plain":
		return renderQRCodePlain(content)
	case "kitty":
		return renderQRKitty(content, tmux)
	case "iterm2":
		return renderQRITerm2(content, tmux)
	case "auto":
		if getenv("NO_COLOR") != "" {
			return renderQRCodePlain(content)
		}
		if isTerminal {
			switch detectImageProtocol(getenv) {
			case protoKitty:
				return renderQRKitty(content, false)
			case protoITerm2:
				return renderQRITerm2(content, false)
			}
		}
		return renderQRCode(content)
	}
	return "", fmt.Errorf("unknown QR mode %q, want one of %s", mode, strings.Join(qrModes, ", "))
}

// isTerminal reports whether w is a terminal, as opposed to a pipe or a file.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
