// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SafeLabel makes a string chosen by the other side of a connection fit
// to print on a console or show on a screen. Control characters (a
// terminal escape sequence, a newline that forges a second log line),
// format characters (a bidirectional override that reorders what follows
// it, a zero-width space) and invalid UTF-8 each become "?", and the
// result is cut to at most maxRunes characters with "..." appended when
// it was cut.
//
// Only authenticated peers can send these strings, so this is hygiene
// rather than a defence against a stranger; a label is still never
// evidence of who sent it.
func SafeLabel(s string, maxRunes int) string {
	var b strings.Builder
	n := 0
	for len(s) > 0 {
		if n == maxRunes {
			b.WriteString("...")
			break
		}
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			b.WriteByte('?')
		} else {
			b.WriteRune(r)
		}
		s = s[size:]
		n++
	}
	return b.String()
}
