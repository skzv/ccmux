package config

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/skzv/ccmux/internal/tomlpatch"
)

// fullRewrite is what Save wrote before it learned to patch: the struct
// encoding plus the unknown keys carried over from prev.
func fullRewrite(t *testing.T, cfg Config, prev []byte) []byte {
	t.Helper()
	if cfg.SchemaVersion < SchemaVersion {
		cfg.SchemaVersion = SchemaVersion
	}
	data, err := encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if merged, ok := carryUnknownKeys(data, prev); ok {
		data = merged
	}
	return data
}

func decodeTOML(t *testing.T, b []byte) map[string]any {
	t.Helper()
	m := map[string]any{}
	if _, err := toml.Decode(string(b), &m); err != nil {
		t.Fatalf("decode: %v\n%s", err, b)
	}
	return m
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSave_KeepsCommentsAndLayout — a config.toml ccmux wrote and the
// user then annotated goes through a Settings change with every
// comment, blank line and key where it was: only the changed value's
// bytes differ. (Save used to re-encode the whole file, dropping all
// of it.)
func TestSave_KeepsCommentsAndLayout(t *testing.T) {
	withFakeHome(t)
	base, err := encode(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	annotated := "# ccmux on the laptop. Hosts live at the bottom.\n\n" + string(base)
	for _, r := range [][2]string{
		{`theme = "catppuccin-mocha"`, `theme = "catppuccin-mocha"   # easy on the eyes`},
		{"[daemon]\n", "# tuned for the mac mini\n[daemon]\n"},
		{"  poll_interval_seconds = 2\n", "  poll_interval_seconds = 2 # lower spins the fans\n\n"},
	} {
		if !strings.Contains(annotated, r[0]) {
			t.Fatalf("fixture drifted: %q not in the encoded defaults", r[0])
		}
		annotated = strings.Replace(annotated, r[0], r[1], 1)
	}
	p := writeConfigFile(t, annotated)

	if _, err := Update(func(c *Config) error { c.Theme = "dracula"; return nil }); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(annotated, `theme = "catppuccin-mocha"`, `theme = "dracula"`, 1)
	if got := readFile(t, p); got != want {
		t.Errorf("save rewrote more than the theme line:\n--- got\n%s\n--- want\n%s", got, want)
	}
}

// TestSave_HandWrittenConfigKeepsItsLines — a short hand-written file
// gains the keys the full encoding carries (that's the data Save has
// always written) but every line the user wrote survives verbatim and
// in order, including unknown keys, and the result decodes to exactly
// what the old full rewrite did. A second save is a no-op.
func TestSave_HandWrittenConfigKeepsItsLines(t *testing.T) {
	withFakeHome(t)
	orig := `# my ccmux config
theme = "nord"  # dark

# the boxes I attach to
[[host]]
name = "mini"      # mac mini in the closet
address = "mini.tail.ts.net"

[agents]
default = "codex" # I live in codex
future_key = "from a newer ccmux"
`
	p := writeConfigFile(t, orig)
	cfg, err := Update(func(c *Config) error { c.Sessions.AttachMode = "exclusive"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)

	rest := got
	for _, line := range strings.Split(strings.TrimSpace(orig), "\n") {
		if line == "" {
			continue
		}
		i := strings.Index(rest, line+"\n")
		if i < 0 {
			t.Fatalf("line %q lost or out of order:\n%s", line, got)
		}
		rest = rest[i+len(line):]
	}
	if want := fullRewrite(t, cfg, []byte(orig)); !tomlpatch.Equal(decodeTOML(t, []byte(got)), decodeTOML(t, want)) {
		t.Errorf("patched config holds different data than the full rewrite:\n--- patched\n%s\n--- full\n%s", got, want)
	}
	if !strings.Contains(got, `attach_mode = "exclusive"`) {
		t.Errorf("the change itself is missing:\n%s", got)
	}
	// Added tables follow Config's field order, even though the unknown
	// key made the full encoding a (sorted) map.
	last := -1
	for _, hdr := range []string{"[projects]", "[sleep]", "[daemon]", "[sessions]", "[apns]", "[openrouter]"} {
		i := strings.Index(got, "\n"+hdr+"\n")
		if i <= last {
			t.Errorf("%s out of Config's field order:\n%s", hdr, got)
		}
		last = i
	}

	if _, err := Update(func(*Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, p); again != got {
		t.Errorf("an unchanged save rewrote the file:\n--- first\n%s\n--- second\n%s", got, again)
	}
}

// TestSave_FallsBackToFullRewrite — when the existing file can't be
// patched, Save writes the full encoding, exactly as before patching
// existed: formatting is lost, data never is.
func TestSave_FallsBackToFullRewrite(t *testing.T) {
	withFakeHome(t)
	patchTOML = func(_, _, _ []byte) ([]byte, error) { return nil, errors.New("cannot patch") }
	t.Cleanup(func() { patchTOML = tomlpatch.PatchOrdered })
	orig := "# comment\ntheme = \"nord\"\nfuture = 1\n"
	p := writeConfigFile(t, orig)
	cfg, err := Update(func(c *Config) error { c.Theme = "gruvbox"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, p), string(fullRewrite(t, cfg, []byte(orig))); got != want {
		t.Errorf("fallback didn't write the full encoding:\n--- got\n%s\n--- want\n%s", got, want)
	}
}

// TestSave_UnpatchableShapeStillSavesEverything — a file shape the
// patcher declines (BurntSushi accepts extending an inline table with a
// [header]; tomlpatch doesn't model it) takes the fallback end to end:
// the change lands and the unknown table survives.
func TestSave_UnpatchableShapeStillSavesEverything(t *testing.T) {
	withFakeHome(t)
	orig := "theme = \"nord\" # c\nx = { a = 1 }\n[x.b]\nc = 2\n"
	p := writeConfigFile(t, orig)
	if _, err := tomlpatch.Patch([]byte(orig), []byte("theme = \"x\"\n")); err == nil {
		t.Fatal("fixture no longer exercises the fallback: tomlpatch patched it")
	}
	if _, err := Update(func(c *Config) error { c.Theme = "gruvbox"; return nil }); err != nil {
		t.Fatal(err)
	}
	got := decodeTOML(t, []byte(readFile(t, p)))
	if got["theme"] != "gruvbox" {
		t.Errorf("theme = %v, want gruvbox", got["theme"])
	}
	x, _ := got["x"].(map[string]any)
	if x == nil || x["a"] != int64(1) {
		t.Errorf("unknown table x lost: %v", got["x"])
	}
}
