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
// it, a zero-width space), line and paragraph separators (U+2028, U+2029,
// which break a line without being control characters) and invalid UTF-8
// each become "?", and the
// result is cut to at most maxRunes characters with "..." appended when
// it was cut.
//
// Only authenticated peers can send these strings, so this is hygiene
// rather than a defence against a stranger; a label is still never
// evidence of who sent it.
func SafeLabel(s string, maxRunes int) string {
	s = tidySpaces(s)
	var b strings.Builder
	n := 0
	for len(s) > 0 {
		if n == maxRunes {
			b.WriteString("...")
			break
		}
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			b.WriteByte('?')
		} else {
			b.WriteRune(r)
		}
		s = s[size:]
		n++
	}
	return b.String()
}

// blankLooking are letters and symbols that draw nothing, though they are not spaces: the Hangul fillers and
// the empty Braille pattern. A label made only of them reads as empty.
const blankLooking = "\u115f\u1160\u3164\uffa0\u2800"

// tidySpaces drops the spaces (category Zs; a newline or a separator is not one, it is marked later) at the
// ends of s, makes each run of spaces one space, and returns "" for a string with nothing visible in it, so that a label cannot be made to look like another by trailing spaces,
// or to look like nothing by invisible letters.
func tidySpaces(s string) string {
	visible := false
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.Is(unicode.Zs, r) {
			space = true
			continue
		}
		if strings.ContainsRune(blankLooking, r) {
			// Kept in a label that has something else in it, and noted as not visible.
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		visible = true
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	if !visible {
		return ""
	}
	return b.String()
}
