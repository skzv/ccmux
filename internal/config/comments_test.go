package config

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/skzv/ccmux/internal/tomlpatch"
)

// fullRewrite is what Save wrote before it learned to patch: the struct
// encoding plus the unknown keys carried over from prev.
func fullRewrite(t *testing.T, cfg Config, prev []byte) []byte {
	t.Helper()
	data, err := encode(forSave(cfg))
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

// mustParse reads config text the way Load does.
func mustParse(t *testing.T, b []byte) Config {
	t.Helper()
	cfg, err := parse(b)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, b)
	}
	return cfg
}

// TestSave_HandWrittenConfigKeepsItsLines — a short hand-written file
// keeps every line the user wrote, verbatim and in order (unknown keys
// included), and gains only what changed plus schema_version: not the
// ~50 lines of defaults the full encoding carries. It still loads to
// exactly what the old full rewrite did. A second save is a no-op.
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
	cfg, err := Update(func(c *Config) error {
		c.Sessions.AttachMode = "exclusive"
		c.Daemon.ListenTailnet = true
		c.OpenRouter.Enabled = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)

	// Added tables follow Config's field order, even though the unknown
	// key made the full encoding a (sorted) map.
	want := strings.Replace(orig, "# dark\n", "# dark\nschema_version = 1\n", 1) + `
[daemon]
listen_tailnet = true

[sessions]
attach_mode = "exclusive"

[openrouter]
enabled = true
`
	if got != want {
		t.Errorf("save added more than the changes:\n--- got\n%s\n--- want\n%s", got, want)
	}
	full := fullRewrite(t, cfg, []byte(orig))
	if !reflect.DeepEqual(mustParse(t, []byte(got)), mustParse(t, full)) {
		t.Errorf("patched config loads differently than the full rewrite:\n--- patched\n%s\n--- full\n%s", got, full)
	}

	if _, err := Update(func(*Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, p); again != got {
		t.Errorf("an unchanged save rewrote the file:\n--- first\n%s\n--- second\n%s", got, again)
	}
}

// TestSave_NewFileGetsEverySetting — only an existing file is trimmed:
// a new (or empty) config.toml still gets every setting written out,
// so there is something to read and edit.
func TestSave_NewFileGetsEverySetting(t *testing.T) {
	withFakeHome(t)
	full, err := encode(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(Defaults()); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != string(full) {
		t.Errorf("new file:\n%s\nwant the full encoding:\n%s", got, full)
	}
	writeConfigFile(t, "\n")
	if err := Save(Defaults()); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != string(full) {
		t.Errorf("empty file:\n%s\nwant the full encoding:\n%s", got, full)
	}
}

// TestSave_OmitsOnlyWhatLoadsTheSame — a setting the file leaves out
// is added only when it no longer holds what Load would give it anyway;
// whatever the file already has stays, even at its default. Each saved
// file loads to exactly what the full rewrite does.
func TestSave_OmitsOnlyWhatLoadsTheSame(t *testing.T) {
	cases := []struct {
		name, orig string
		change     func(*Config)
		want       string
	}{{
		name:   "a default-true setting turned off",
		orig:   "theme = \"nord\"\n",
		change: func(c *Config) { c.Notes.AutoLogSessions = false },
		want:   "theme = \"nord\"\nschema_version = 1\n\n[notes]\nauto_log_sessions = false\n",
	}, {
		name:   "one agent's command: just its table",
		orig:   "[agents]\ndefault = \"codex\"\n",
		change: func(c *Config) { c.Agents.Codex.Command = "/opt/codex" },
		want:   "schema_version = 1\n\n[agents]\ndefault = \"codex\"\n\n[agents.codex]\ncommand = \"/opt/codex\"\n",
	}, {
		name:   "a written key set back to its default stays written",
		orig:   "schema_version = 1\n[daemon]\npoll_interval_seconds = 5 # busy box\n",
		change: func(c *Config) { c.Daemon.PollIntervalSeconds = 2 },
		want:   "schema_version = 1\n[daemon]\npoll_interval_seconds = 2 # busy box\n",
	}, {
		name:   "an explicit api tier, which needs schema_version to mean it",
		orig:   "theme = \"nord\"\n",
		change: func(c *Config) { c.Subscription.Tier = "api" },
		want:   "theme = \"nord\"\nschema_version = 1\n\n[subscription]\ntier = \"api\"\n",
	}, {
		name: "a new host: only the fields it sets",
		orig: "# hosts\n[[host]]\nname = \"mini\"\naddress = \"1.1.1.1\"\n",
		change: func(c *Config) {
			c.Hosts = append(c.Hosts, Host{Name: "air", Address: "2.2.2.2", Port: 7474, Mosh: true})
		},
		want: "schema_version = 1\n\n# hosts\n[[host]]\nname = \"mini\"\naddress = \"1.1.1.1\"\n\n[[host]]\nname = \"air\"\naddress = \"2.2.2.2\"\nport = 7474\nmosh = true\n",
	}, {
		name: "each host keeps its own keys: one spelling out zeros doesn't spread them",
		orig: "schema_version = 1\n[[host]]\nname = \"mini\"\naddress = \"1.1.1.1\"\nuser = \"\"\nport = 7474\nssh_port = 0\nmosh = false\n\n[[host]]\nname = \"pi\"\naddress = \"3.3.3.3\"\n",
		change: func(c *Config) {
			c.Hosts = append(c.Hosts, Host{Name: "air", Address: "2.2.2.2", Mosh: true})
		},
		want: "schema_version = 1\n[[host]]\nname = \"mini\"\naddress = \"1.1.1.1\"\nuser = \"\"\nport = 7474\nssh_port = 0\nmosh = false\n\n[[host]]\nname = \"pi\"\naddress = \"3.3.3.3\"\n\n[[host]]\nname = \"air\"\naddress = \"2.2.2.2\"\nmosh = true\n",
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withFakeHome(t)
			p := writeConfigFile(t, c.orig)
			cfg, err := Update(func(cfg *Config) error { c.change(cfg); return nil })
			if err != nil {
				t.Fatal(err)
			}
			got := readFile(t, p)
			if got != c.want {
				t.Errorf("got\n%s\nwant\n%s", got, c.want)
			}
			if full := fullRewrite(t, cfg, []byte(c.orig)); !reflect.DeepEqual(mustParse(t, []byte(got)), mustParse(t, full)) {
				t.Errorf("loads differently than the full rewrite:\n--- got\n%s\n--- full\n%s", got, full)
			}
		})
	}
}

// TestSave_TrimmedConfigsLoadLikeTheFullRewrite — whatever a file
// leaves out and whatever a save changes, leaving defaults out never
// changes what loads: the saved file loads to exactly what the full
// rewrite would, and saving again is a no-op.
func TestSave_TrimmedConfigsLoadLikeTheFullRewrite(t *testing.T) {
	changes := []func(*Config, *rand.Rand){
		func(c *Config, r *rand.Rand) { c.Theme = []string{"nord", "catppuccin-mocha"}[r.Intn(2)] },
		func(c *Config, r *rand.Rand) { c.Notes.AutoLogSessions = r.Intn(2) == 0 },
		func(c *Config, r *rand.Rand) { c.Daemon.TailnetPort = []int{7474, 7575, 0}[r.Intn(3)] },
		func(c *Config, r *rand.Rand) { c.Sleep.Mode = []string{"safe", "dangerous", ""}[r.Intn(3)] },
		func(c *Config, r *rand.Rand) { c.Agents.Cursor.Command = []string{"", "/opt/cursor"}[r.Intn(2)] },
		func(c *Config, r *rand.Rand) {
			c.Subscription.SetTierFor([]string{"claude", "codex"}[r.Intn(2)], "api")
		},
		func(c *Config, r *rand.Rand) { c.OpenRouter.RouteAgents = [][]string{nil, {"codex"}}[r.Intn(2)] },
		func(c *Config, r *rand.Rand) {
			c.Hosts = append(c.Hosts, Host{Name: "new", Address: "9.9.9.9", Port: r.Intn(2) * 7474, Mosh: r.Intn(2) == 0})
		},
		func(c *Config, r *rand.Rand) {
			if len(c.Hosts) > 0 {
				i := r.Intn(len(c.Hosts))
				c.Hosts = append(c.Hosts[:i:i], c.Hosts[i+1:]...)
			}
		},
		func(c *Config, r *rand.Rand) {
			if len(c.Hosts) > 0 {
				c.Hosts[r.Intn(len(c.Hosts))].User = []string{"", "me"}[r.Intn(2)]
			}
		},
	}
	rounds := 150
	if testing.Short() {
		rounds = 30
	}
	r := rand.New(rand.NewSource(20260928))
	for i := 0; i < rounds; i++ {
		withFakeHome(t)
		full := Defaults()
		for h := 0; h < r.Intn(4); h++ {
			full.Hosts = append(full.Hosts, Host{
				Name: fmt.Sprintf("h%d", h), Address: fmt.Sprintf("10.0.0.%d", h),
				User: []string{"", "me"}[r.Intn(2)], Port: r.Intn(2) * 7474, Mosh: r.Intn(2) == 0,
			})
		}
		b, err := encode(full)
		if err != nil {
			t.Fatal(err)
		}
		m := decodeTOML(t, b)
		trim(r, m, true)
		orig, err := encode(m)
		if err != nil {
			t.Fatal(err)
		}
		p := writeConfigFile(t, "# trimmed by hand\n"+string(orig))
		cfg, err := Update(func(c *Config) error {
			for n := 1 + r.Intn(3); n > 0; n-- {
				changes[r.Intn(len(changes))](c, r)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		got := readFile(t, p)
		if want := fullRewrite(t, cfg, orig); !reflect.DeepEqual(mustParse(t, []byte(got)), mustParse(t, want)) {
			t.Fatalf("round %d loads differently than the full rewrite:\n--- original\n%s\n--- saved\n%s\n--- full\n%s", i, orig, got, want)
		}
		if _, err := Update(func(*Config) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if again := readFile(t, p); again != got {
			t.Fatalf("round %d: an unchanged save rewrote the file:\n--- first\n%s\n--- second\n%s", i, got, again)
		}
	}
}

// trim deletes random keys and tables from m (never a host's name).
func trim(r *rand.Rand, m map[string]any, root bool) {
	for _, k := range sortedKeys(m) {
		if k == "name" && !root {
			continue
		}
		if r.Intn(10) < 4 {
			delete(m, k)
			continue
		}
		if t, ok := m[k].(map[string]any); ok {
			trim(r, t, false)
		}
		for _, t := range tableList(m[k]) {
			trim(r, t, false)
		}
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
