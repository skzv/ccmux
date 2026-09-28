package tomlpatch

import (
	"math/rand"
	"testing"

	"github.com/BurntSushi/toml"
)

// FuzzPatch drives the grammar-based property in gen_test.go: for a
// random commented TOML document and a random change to its data, the
// patch succeeds, decodes to exactly the changed data, keeps every
// comment and untouched line, and re-patching is a no-op.
func FuzzPatch(f *testing.F) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 32; i++ {
		b := make([]byte, 64+rng.Intn(192))
		rng.Read(b)
		f.Add(b)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		checkProperty(t, data)
	})
}

// FuzzPatchArbitrary feeds Patch arbitrary (original, target) pairs:
// it must never panic, and whenever it returns text, that text decodes
// to exactly what target decodes to.
func FuzzPatchArbitrary(f *testing.F) {
	for _, s := range [][2]string{
		{"a = 1 # c\n[t]\nb = 2\n", "a = 2\n[t]\nb = 3\nc = 4\n"},
		{"[[x]]\nn = 1\n[[x]]\nn = 2\n", "[[x]]\nn = 2\n"},
		{"d.x = 1\n[d.y]\nz = 2\n", "d = 5\n"},
		{"[a.b]\nc = 1\n", "[a]\nk = 1\n[a.b]\nc = 1\n"},
		{"x = { a = 1 }\n", "[x]\na = 2\n"},
		{"\uFEFFa = 1\r\n", "a = 2\n"},
		{"# only a comment", "k = 'v'\n"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, original, target string) {
		out, err := patch([]byte(original), []byte(target), nil)
		if err != nil {
			return
		}
		var got, want map[string]any
		if _, err := toml.Decode(string(out), &got); err != nil {
			t.Fatalf("patched text does not decode: %v\n%q", err, out)
		}
		if _, err := toml.Decode(target, &want); err != nil {
			t.Fatalf("patch accepted an undecodable target: %v", err)
		}
		if !Equal(orEmpty(got), orEmpty(want)) {
			t.Fatalf("patched text decodes to %v, want %v", got, want)
		}
	})
}

// TestPatch_Property runs the FuzzPatch property over a few thousand
// pseudo-random documents on every `go test`, so the invariant is
// exercised without a fuzzing run.
func TestPatch_Property(t *testing.T) {
	n := 10000
	if testing.Short() {
		n = 1000
	}
	rng := rand.New(rand.NewSource(20260928))
	fallbacks := 0
	for i := 0; i < n; i++ {
		b := make([]byte, 32+rng.Intn(480))
		rng.Read(b)
		if checkProperty(t, b) {
			fallbacks++
		}
		if t.Failed() {
			return
		}
	}
	// Falling back loses the user's formatting, so it has to stay the
	// rare exception (today: BurntSushi misreading dotted keys in arrays
	// of tables), not a common outcome.
	if fallbacks*100 > n {
		t.Errorf("%d of %d patches fell back to a full rewrite", fallbacks, n)
	}
	t.Logf("%d of %d patches fell back (decoder quirk)", fallbacks, n)
}
