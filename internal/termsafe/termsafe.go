// Package termsafe strips terminal control sequences from untrusted
// text before it reaches the terminal.
//
// ccmux renders text it did not write — conversation transcripts,
// markdown notes from cloned repositories, ripgrep snippets — through
// lipgloss and glamour, which pass escape sequences straight through.
// A README carrying an OSC 52 sequence could overwrite the clipboard
// just by being previewed in the Notes tab, and a stray CSI in a
// prompt breaks row layout. Strip removes every such sequence so data
// layers can hand display-safe text to the TUI.
package termsafe

import (
	"unicode/utf8"
)

const (
	esc = 0x1b
	bel = 0x07
)

// String returns s with terminal control sequences and control
// characters removed:
//
//   - ESC-initiated sequences: CSI (ESC [ … final), OSC (ESC ] … BEL
//     or ST), DCS / SOS / PM / APC (ESC P / X / ^ / _ … ST), and the
//     short ESC + intermediates + final forms (ESC ( B, ESC 7, …).
//     An unterminated string sequence swallows the rest of the input —
//     emitting its payload as text would be just as unsafe.
//   - The 8-bit C1 equivalents (U+009B CSI, U+009D OSC, U+0090 DCS,
//     U+0098 SOS, U+009E PM, U+009F APC) with the same bodies, and
//     every other C1 control (U+0080–U+009F).
//   - Every other C0 control and DEL, except '\n' and '\t'. '\r' is
//     dropped too, so CRLF text comes out as LF.
//
// Invalid UTF-8 bytes are replaced with U+FFFD so a terminal in an
// 8-bit mode can't read a raw 0x9B byte as CSI. Text that needs no
// changes is returned as is, without allocating.
func String(s string) string {
	if clean(s) {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == esc:
			i = skipEscape(s, i+1)
			continue
		case c == '\n' || c == '\t':
			out = append(out, c)
			i++
			continue
		case c < 0x20 || c == 0x7f:
			i++
			continue
		case c < utf8.RuneSelf:
			out = append(out, c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			out = utf8.AppendRune(out, utf8.RuneError)
			i++
			continue
		}
		if r >= 0x80 && r <= 0x9f {
			i = skipC1(s, r, i+size)
			continue
		}
		out = append(out, s[i:i+size]...)
		i += size
	}
	return string(out)
}

// Bytes is String for byte slices. The result never aliases b when a
// change was needed; when b is already clean it is returned as is.
func Bytes(b []byte) []byte {
	if clean(string(b)) {
		return b
	}
	return []byte(String(string(b)))
}

// clean reports whether s is valid UTF-8 with no bytes String would
// drop or rewrite — the no-allocation fast path.
func clean(s string) bool {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if (c < 0x20 && c != '\n' && c != '\t') || c == 0x7f {
				return false
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || (r >= 0x80 && r <= 0x9f) {
			return false
		}
		i += size
	}
	return true
}

// skipEscape returns the index just past the escape sequence whose
// ESC byte sits immediately before s[i].
func skipEscape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch c := s[i]; c {
	case '[':
		return skipCSI(s, i+1)
	case ']':
		return skipString(s, i+1, true)
	case 'P', 'X', '^', '_':
		return skipString(s, i+1, false)
	default:
		// nF / Fp / Fe / Fs forms: ESC, any intermediates (0x20–0x2F),
		// then one final byte (0x30–0x7E).
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
			i++
		}
		return i
	}
}

// skipC1 handles an 8-bit C1 control r whose encoding ends just before
// s[i]: the introducers get the same bodies as their ESC forms, every
// other C1 control is dropped alone.
func skipC1(s string, r rune, i int) int {
	switch r {
	case 0x9b: // CSI
		return skipCSI(s, i)
	case 0x9d: // OSC
		return skipString(s, i, true)
	case 0x90, 0x98, 0x9e, 0x9f: // DCS, SOS, PM, APC
		return skipString(s, i, false)
	}
	return i
}

// skipCSI skips parameter bytes (0x30–0x3F), intermediate bytes
// (0x20–0x2F) and the final byte (0x40–0x7E) of a control sequence. A
// byte outside those ranges ends the sequence early without being
// consumed — it is ordinary text again (or another control that the
// main loop will strip).
func skipCSI(s string, i int) int {
	for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
		i++
	}
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
		i++
	}
	return i
}

// skipString skips a control-string body up to and including its
// terminator: ST (ESC \ or U+009C), or BEL when belEnds (xterm accepts
// BEL as the OSC terminator). An unterminated body runs to the end.
func skipString(s string, i int, belEnds bool) int {
	for i < len(s) {
		c := s[i]
		switch {
		case c == esc:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
			// A new ESC aborts the string in most terminals; treat it
			// as the start of the next sequence.
			return i
		case c == bel && belEnds:
			return i + 1
		case c == 0xc2 && i+1 < len(s) && s[i+1] == 0x9c: // U+009C ST
			return i + 2
		}
		i++
	}
	return i
}
