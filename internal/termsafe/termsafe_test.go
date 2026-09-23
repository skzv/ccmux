package termsafe

import (
	"bytes"
	"testing"
	"unicode/utf8"
)

func TestString(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "hello world", "hello world"},
		{"keeps newline and tab", "a\tb\nc", "a\tb\nc"},
		{"keeps unicode", "héllo — 世界 🎉", "héllo — 世界 🎉"},
		{"crlf to lf", "line1\r\nline2\r\n", "line1\nline2\n"},
		{"sgr color", "\x1b[1;31mred\x1b[0m text", "red text"},
		{"sgr from claude stdout", "Set model to \x1b[1mFable 5\x1b[22m and saved", "Set model to Fable 5 and saved"},
		{"cursor movement", "a\x1b[2Jb\x1b[10;20Hc", "abc"},
		{"private csi", "x\x1b[?1049hy", "xy"},
		{"osc 52 clipboard with BEL", "before\x1b]52;c;ZWNobyBwd25lZA==\x07after", "beforeafter"},
		{"osc 52 clipboard with ST", "before\x1b]52;c;ZWNobyBwd25lZA==\x1b\\after", "beforeafter"},
		{"osc 8 hyperlink", "\x1b]8;;https://evil.example\x1b\\click\x1b]8;;\x1b\\", "click"},
		{"osc title", "\x1b]0;pwned\x07ok", "ok"},
		{"unterminated osc eats rest", "ok\x1b]52;c;AAAA", "ok"},
		{"dcs", "a\x1bPq#0;2;0;0;0\x1b\\b", "ab"},
		{"apc", "a\x1b_payload\x1b\\b", "ab"},
		{"pm and sos", "a\x1b^pm\x1b\\b\x1bXsos\x1b\\c", "abc"},
		{"charset designation", "a\x1b(Bb", "ab"},
		{"save cursor", "a\x1b7b\x1b8c", "abc"},
		{"reset", "a\x1bcb", "ab"},
		{"lone esc at end", "abc\x1b", "abc"},
		{"esc esc csi", "a\x1b\x1b[31mb", "ab"},
		{"osc aborted by new escape", "a\x1b]0;t\x1b[31mb", "ab"},
		{"c0 controls", "a\x00b\x01c\x07d\x08e\x0bf\x0cg\x7fh", "abcdefgh"},
		{"c1 csi rune", "a\u009b31mb", "ab"},
		{"c1 osc rune", "a\u009d52;c;AAAA\u009cb", "ab"},
		{"c1 osc rune BEL", "a\u009d0;t\x07b", "ab"},
		{"c1 dcs rune", "a\u0090data\u009cb", "ab"},
		{"other c1 dropped", "a\u0085b\u0080c", "abc"},
		{"raw 0x9b byte replaced", "a\x9b31mb", "a�31mb"},
		{"invalid utf8 replaced", "a\xffb", "a�b"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := String(tc.in); got != tc.want {
				t.Errorf("String(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got := string(Bytes([]byte(tc.in))); got != tc.want {
				t.Errorf("Bytes(%q) = %q, want %q", tc.in, got, tc.want)
			}
			assertSafe(t, tc.in, String(tc.in))
		})
	}
}

func TestString_CleanInputNotCopied(t *testing.T) {
	b := []byte("already clean\n")
	if got := Bytes(b); &got[0] != &b[0] {
		t.Error("Bytes copied an already-clean slice")
	}
}

// assertSafe is the invariant both the table test and the fuzzer
// hold String to.
func assertSafe(t *testing.T, in, out string) {
	t.Helper()
	if !utf8.ValidString(out) {
		t.Fatalf("String(%q) = %q: invalid UTF-8", in, out)
	}
	for _, r := range out {
		switch {
		case r == '\n' || r == '\t':
		case r < 0x20 || r == 0x7f:
			t.Fatalf("String(%q) = %q: kept C0 control %U", in, out, r)
		case r >= 0x80 && r <= 0x9f:
			t.Fatalf("String(%q) = %q: kept C1 control %U", in, out, r)
		}
	}
	if again := String(out); again != out {
		t.Fatalf("String not idempotent: %q -> %q -> %q", in, out, again)
	}
	if !bytes.Equal(Bytes([]byte(in)), []byte(out)) {
		t.Fatalf("Bytes(%q) disagrees with String", in)
	}
}

func FuzzString(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "\x1b[31mred\x1b[0m", "\x1b]52;c;AAAA\x07", "\x1b]8;;u\x1b\\t",
		"\x1bPdcs\x1b\\", "\u009b1m", "\u009d0;x\u009c", "\xc2", "\x1b", "a\r\nb", "\x9b\xff",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		assertSafe(t, in, String(in))
	})
}
