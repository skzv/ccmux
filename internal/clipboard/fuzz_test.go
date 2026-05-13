package clipboard

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

// FuzzOSC52RoundTrip exercises the OSC 52 wire format with arbitrary
// payloads. Contract:
//
//  1. WriteOSC52 never panics for any payload.
//  2. The output is framed: `\x1b]52;c;<base64>\x07`.
//  3. The base64 in the middle decodes back to the original payload
//     byte-for-byte. base64 is binary-safe by design but if the
//     framing ever accidentally injects a stray byte (e.g. someone
//     "cleans up" Fprintf to use %s on a []byte that contains BEL),
//     this test catches it.
//
// Why this matters: OSC 52 is how ccmux's clipboard reaches the
// outer terminal across SSH. A silent breakage means selections on
// a remote pane never land on the local clipboard — exactly the kind
// of bug that's invisible until a user complains.
func FuzzOSC52RoundTrip(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("hello world"),
		[]byte{0x00, 0x01, 0xff, 0x7f},
		[]byte("\x1b]52;c;hijack\x07"), // payload that LOOKS like another OSC 52 sequence
		[]byte("a\nb\x00c\rd"),
		bytes.Repeat([]byte{0xff}, 8192), // 8 KB payload — OSC 52's upper bound is terminal-dependent
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		var buf bytes.Buffer
		if err := WriteOSC52(&buf, string(payload)); err != nil {
			t.Fatalf("WriteOSC52 returned err for payload of %d bytes: %v", len(payload), err)
		}
		out := buf.String()
		if !strings.HasPrefix(out, "\x1b]52;c;") {
			t.Fatalf("missing OSC 52 prefix in output of length %d", len(out))
		}
		if !strings.HasSuffix(out, "\x07") {
			t.Fatalf("missing BEL terminator in output of length %d", len(out))
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(out, "\x1b]52;c;"), "\x07")
		decoded, err := base64.StdEncoding.DecodeString(mid)
		if err != nil {
			t.Fatalf("base64 decode failed: %v (mid=%q)", err, mid)
		}
		if !bytes.Equal(decoded, payload) {
			t.Fatalf("round-trip mismatch: in=%q out=%q", payload, decoded)
		}
	})
}
