package tomlpatch

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if _, err := toml.Decode(s, &m); err != nil {
		t.Fatalf("decode: %v\n%s", err, s)
	}
	return orEmpty(m)
}

// retarget decodes original, lets change edit the data, and returns the
// full re-encode of the result — the target a caller would pass.
func retarget(t *testing.T, original string, change func(m map[string]any)) []byte {
	t.Helper()
	m := decode(t, original)
	change(m)
	return encodeTarget(t, m)
}

// mustPatch patches and checks the core invariant on the way.
func mustPatch(t *testing.T, original string, target []byte) string {
	t.Helper()
	out, err := Patch([]byte(original), target)
	if err != nil {
		t.Fatalf("Patch: %v\n--- original\n%s\n--- target\n%s", err, original, target)
	}
	if got, want := decode(t, string(out)), decode(t, string(target)); !Equal(got, want) {
		t.Fatalf("patched text decodes to %v, want %v\n%s", got, want, out)
	}
	return string(out)
}

func sub(m map[string]any, k string) map[string]any { return m[k].(map[string]any) }

func TestPatch_ChangedValueOnly(t *testing.T) {
	orig := `# ccmux config, tuned by hand
theme = "nord"      # the dark one

[daemon]
# poll faster on this box
poll_interval_seconds = 1
listen_tailnet = true
`
	target := retarget(t, orig, func(m map[string]any) { m["theme"] = "dracula" })
	want := strings.Replace(orig, `"nord"`, `"dracula"`, 1)
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatch_NoChangeIsByteIdentical(t *testing.T) {
	orig := "a   =   'x'  # keep my spacing\r\n\r\n[t]\r\nb = 0x10\r\n"
	target := retarget(t, orig, func(map[string]any) {})
	if got := mustPatch(t, orig, target); got != orig {
		t.Errorf("unchanged data rewrote the file:\n%q", got)
	}
}

func TestPatch_AddsKeyAtEndOfItsTable(t *testing.T) {
	orig := `[agents]
default = "codex" # I live in codex

# notes about [claude]
[claude]
`
	target := retarget(t, orig, func(m map[string]any) {
		sub(m, "agents")["new"] = int64(5)
	})
	want := `[agents]
default = "codex" # I live in codex
new = 5

# notes about [claude]
[claude]
`
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatch_RootKeyGoesAboveFirstTable(t *testing.T) {
	orig := `# header comment

# about agents
[agents]
default = "codex"
`
	target := retarget(t, orig, func(m map[string]any) { m["schema_version"] = int64(1) })
	want := `# header comment

schema_version = 1

# about agents
[agents]
default = "codex"
`
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatch_MissingTableCreated(t *testing.T) {
	orig := "theme = \"nord\" # mine\n"
	target := retarget(t, orig, func(m map[string]any) {
		m["sleep"] = map[string]any{"mode": "safe", "low_battery_cutoff": int64(20)}
	})
	got := mustPatch(t, orig, target)
	if !strings.HasPrefix(got, orig+"\n[sleep]\n") {
		t.Errorf("new table not appended after the original text:\n%s", got)
	}
}

func TestPatch_SubtableGoesAfterItsParent(t *testing.T) {
	orig := `[agents]
default = "codex"

[update]
auto_check = true
`
	target := retarget(t, orig, func(m map[string]any) {
		sub(m, "agents")["claude"] = map[string]any{}
	})
	want := `[agents]
default = "codex"

[agents.claude]

[update]
auto_check = true
`
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// A file ccmux itself wrote indents keys and sub-tables; new sections
// follow suit.
func TestPatch_FollowsFileIndentation(t *testing.T) {
	orig := `[agents]
  default = "claude"
  [agents.claude]
`
	target := retarget(t, orig, func(m map[string]any) {
		sub(m, "agents")["codex"] = map[string]any{"command": "/opt/codex"}
	})
	want := `[agents]
  default = "claude"
  [agents.claude]

  [agents.codex]
    command = "/opt/codex"
`
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatch_ImplicitTableGetsItsOwnHeader(t *testing.T) {
	orig := `# profiles
[profiles.fast]
model = "o4-mini"
`
	target := retarget(t, orig, func(m map[string]any) {
		sub(m, "profiles")["default"] = "fast"
	})
	want := `[profiles]
default = "fast"

# profiles
[profiles.fast]
model = "o4-mini"
`
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatch_DeletedKeyLineRemoved(t *testing.T) {
	orig := `model = "o3"
# effort: how hard to think
model_reasoning_effort = "high"   # was medium
sandbox_mode = "workspace-write"
`
	target := retarget(t, orig, func(m map[string]any) { delete(m, "model_reasoning_effort") })
	want := `model = "o3"
# effort: how hard to think
sandbox_mode = "workspace-write"
`
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatch_DeletedTableKeepsNextTablesComment(t *testing.T) {
	orig := `[a]
x = 1

[subscription.tiers] # per agent
codex = "plus"
# the next table's comment
[b]
y = 2
`
	target := retarget(t, orig, func(m map[string]any) { delete(m, "subscription") })
	want := `[a]
x = 1

# the next table's comment
[b]
y = 2
`
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatch_DottedKeys(t *testing.T) {
	orig := `tui.theme = "dark"  # dotted
tui.notifications = true
other = 1
`
	target := retarget(t, orig, func(m map[string]any) {
		tui := sub(m, "tui")
		tui["theme"] = "light"
		delete(tui, "notifications")
		tui["animations"] = false
	})
	want := `tui.theme = "light"  # dotted
tui.animations = false
other = 1
`
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatch_InlineTableRewrittenWhole(t *testing.T) {
	orig := "[mcp_servers]\ndocs = { command = 'npx', args = ['-y', 'docs'] } # keep\n"
	target := retarget(t, orig, func(m map[string]any) {
		sub(sub(m, "mcp_servers"), "docs")["command"] = "bunx"
	})
	want := "[mcp_servers]\ndocs = { args = [\"-y\", \"docs\"], command = \"bunx\" } # keep\n"
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatch_ArrayOfTables(t *testing.T) {
	orig := `# my machines
[[host]]
name = "mini"   # the mac mini
address = "mini.ts.net"

[[host]]
name = "air"
address = "air.ts.net"   # laptop
`
	t.Run("edit one element in place", func(t *testing.T) {
		target := retarget(t, orig, func(m map[string]any) {
			m["host"].([]map[string]any)[1]["address"] = "air2.ts.net"
		})
		want := strings.Replace(orig, `"air.ts.net"`, `"air2.ts.net"`, 1)
		if got := mustPatch(t, orig, target); got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("append", func(t *testing.T) {
		target := retarget(t, orig, func(m map[string]any) {
			m["host"] = append(m["host"].([]map[string]any), map[string]any{"name": "pi", "address": "pi.ts.net"})
		})
		want := orig + "\n[[host]]\naddress = \"pi.ts.net\"\nname = \"pi\"\n"
		if got := mustPatch(t, orig, target); got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("remove the first", func(t *testing.T) {
		target := retarget(t, orig, func(m map[string]any) {
			m["host"] = m["host"].([]map[string]any)[1:]
		})
		want := `# my machines
[[host]]
name = "air"
address = "air.ts.net"   # laptop
`
		if got := mustPatch(t, orig, target); got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
}

// threeHosts is a hand-written host list, each entry documented.
const threeHosts = `theme = "nord"

# first host
[[host]]
name = "a"
address = "a.ts.net"

# second host (keep this comment)
[[host]]
name = "b"
address = "b.ts.net"

# third host
[[host]]
name = "c"
address = "c.ts.net"

[setup]
completed = true
`

// withDefaults drops the host named drop and gives every remaining one
// the keys a config save fills in — so no element is left unchanged.
func withDefaults(drop string) func(map[string]any) {
	return func(m map[string]any) {
		var out []map[string]any
		for _, h := range m["host"].([]map[string]any) {
			if h["name"] == drop {
				continue
			}
			h["user"], h["port"], h["ssh_port"], h["mosh"] = "", int64(0), int64(0), false
			out = append(out, h)
		}
		m["host"] = out
	}
}

// TestPatch_ArrayOfTables_RemoveWhileAddingKeys — removing a host in a
// save that also fills in every host's missing keys used to pair the
// hosts by position (none was unchanged), rewriting b into c: b's
// comment ended up above c, and c's dangled above [setup]. Elements now
// match by identity, and a removed one takes its comment with it.
func TestPatch_ArrayOfTables_RemoveWhileAddingKeys(t *testing.T) {
	const added = "mosh = false\nport = 0\nssh_port = 0\nuser = \"\"\n" // a re-encoded map: alphabetical
	t.Run("middle", func(t *testing.T) {
		want := `theme = "nord"

# first host
[[host]]
name = "a"
address = "a.ts.net"
` + added + `
# third host
[[host]]
name = "c"
address = "c.ts.net"
` + added + `
[setup]
completed = true
`
		if got := mustPatch(t, threeHosts, retarget(t, threeHosts, withDefaults("b"))); got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("first, CRLF", func(t *testing.T) {
		orig := strings.ReplaceAll(threeHosts, "\n", "\r\n")
		want := strings.ReplaceAll(`theme = "nord"

# second host (keep this comment)
[[host]]
name = "b"
address = "b.ts.net"
`+added+`
# third host
[[host]]
name = "c"
address = "c.ts.net"
`+added+`
[setup]
completed = true
`, "\n", "\r\n")
		if got := mustPatch(t, orig, retarget(t, orig, withDefaults("a"))); got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})
}

// TestPatch_ArrayOfTables_RemovedElementTakesItsComment — with no key
// added, the removed element still used to leave its comment behind,
// above the next element's own.
func TestPatch_ArrayOfTables_RemovedElementTakesItsComment(t *testing.T) {
	drop := func(name string) func(map[string]any) {
		return func(m map[string]any) {
			var out []map[string]any
			for _, h := range m["host"].([]map[string]any) {
				if h["name"] != name {
					out = append(out, h)
				}
			}
			m["host"] = out
		}
	}
	for _, c := range []struct{ name, orig, drop, want string }{{
		name: "first, at the top of the file",
		orig: "# first host\n[[host]]\nname = \"a\"\n\n# second host\n[[host]]\nname = \"b\"\n",
		drop: "a",
		want: "# second host\n[[host]]\nname = \"b\"\n",
	}, {
		name: "last: no dangling comment or trailing blank line",
		orig: "x = 1\n\n# first host\n[[host]]\nname = \"a\"\n\n# second host\n[[host]]\nname = \"b\"\n",
		drop: "b",
		want: "x = 1\n\n# first host\n[[host]]\nname = \"a\"\n",
	}, {
		name: "last, before a footer comment set apart by a blank line",
		orig: "x = 1\n\n# first host\n[[host]]\nname = \"a\"\n# second host\n[[host]]\nname = \"b\"\n# b's own note\n\n# end of file\n",
		drop: "b",
		want: "x = 1\n\n# first host\n[[host]]\nname = \"a\"\n\n# end of file\n",
	}, {
		name: "the only one: its comment doesn't move onto the next table",
		orig: "x = 1\n\n# the only host\n[[host]]\nname = \"a\"\n\n# agent settings\n[agents]\ndefault = \"codex\"\n",
		drop: "a",
		want: "x = 1\n\n# agent settings\n[agents]\ndefault = \"codex\"\n",
	}} {
		t.Run(c.name, func(t *testing.T) {
			target := retarget(t, c.orig, func(m map[string]any) {
				drop(c.drop)(m)
				if len(m["host"].([]map[string]any)) == 0 {
					delete(m, "host")
				}
			})
			if got := mustPatch(t, c.orig, target); got != c.want {
				t.Errorf("got\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

// A comment above the first element documents the whole list when no
// other element has a comment of its own (TestPatch_ArrayOfTables'
// "remove the first"); new elements go under it too.
func TestPatch_ArrayOfTables_ListCommentStays(t *testing.T) {
	orig := "# my machines\n[[host]]\nname = \"mini\"\n\n[[host]]\nname = \"air\"\n"
	target := retarget(t, orig, func(m map[string]any) {
		m["host"] = []map[string]any{{"name": "pi"}}
	})
	want := "# my machines\n[[host]]\nname = \"pi\"\n"
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("replacing every element: got %q, want %q", got, want)
	}
	target = retarget(t, orig, func(m map[string]any) {
		m["host"] = append([]map[string]any{{"name": "pi"}}, m["host"].([]map[string]any)...)
	})
	want = "# my machines\n[[host]]\nname = \"pi\"\n\n[[host]]\nname = \"mini\"\n\n[[host]]\nname = \"air\"\n"
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("adding in front: got %q, want %q", got, want)
	}
}

func TestPatch_ArrayOfTables_EditedElementKeepsItsComment(t *testing.T) {
	orig := "# the mini\n[[host]]\nname = \"mini\"\naddress = \"1.1.1.1\" # static\n\n# the air\n[[host]]\nname = \"air\"\naddress = \"2.2.2.2\"\n"
	t.Run("renamed: address still identifies it", func(t *testing.T) {
		target := retarget(t, orig, func(m map[string]any) {
			m["host"].([]map[string]any)[0]["name"] = "mini2"
		})
		want := strings.Replace(orig, `"mini"`, `"mini2"`, 1)
		if got := mustPatch(t, orig, target); got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("replaced: a different host takes none of its comments", func(t *testing.T) {
		target := retarget(t, orig, func(m map[string]any) {
			m["host"].([]map[string]any)[0] = map[string]any{"name": "pi", "address": "3.3.3.3"}
		})
		want := "[[host]]\naddress = \"3.3.3.3\"\nname = \"pi\"\n\n# the air\n[[host]]\nname = \"air\"\naddress = \"2.2.2.2\"\n"
		if got := mustPatch(t, orig, target); got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("lone element: edited in place", func(t *testing.T) {
		orig := "[[x]] # the one\nn = 1 # count\n"
		target := retarget(t, orig, func(m map[string]any) { m["x"].([]map[string]any)[0]["n"] = int64(2) })
		if got, want := mustPatch(t, orig, target), "[[x]] # the one\nn = 2 # count\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// TestPatch_DeletedTableTakesItsComment — the comment directly above a
// deleted table goes with it; one set apart by a blank line, or one
// opening the file, stays.
func TestPatch_DeletedTableTakesItsComment(t *testing.T) {
	for _, c := range []struct{ orig, want string }{
		{"a = 1\n\n# per-agent tiers\n[tiers]\ncodex = \"plus\"\n\n[b]\ny = 2\n", "a = 1\n\n[b]\ny = 2\n"},
		{"a = 1\n\n# about tiers\n\n[tiers]\ncodex = \"plus\"\n\n[b]\ny = 2\n", "a = 1\n\n# about tiers\n\n[b]\ny = 2\n"},
		{"# my config\n[tiers]\ncodex = \"plus\"\n\n[b]\ny = 2\n", "# my config\n[b]\ny = 2\n"},
	} {
		target := retarget(t, c.orig, func(m map[string]any) { delete(m, "tiers") })
		if got := mustPatch(t, c.orig, target); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestAlignTables(t *testing.T) {
	h := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	cases := []struct {
		name   string
		ol, nl []map[string]any
		want   [][2]int
	}{
		{"removed, keys added to the rest",
			[]map[string]any{h("name", "a"), h("name", "b"), h("name", "c")},
			[]map[string]any{h("name", "a", "port", int64(0)), h("name", "c", "port", int64(0))},
			[][2]int{{0, 0}, {2, 1}}},
		{"identity wins over look-alikes",
			[]map[string]any{h("name", "a", "user", "me", "mosh", true), h("name", "b", "user", "me", "mosh", true)},
			[]map[string]any{h("name", "b", "user", "me", "mosh", true)},
			[][2]int{{1, 0}}},
		{"no shared name: most-alike",
			[]map[string]any{h("k", "x", "v", int64(1)), h("k", "x", "v", int64(2))},
			[]map[string]any{h("k", "x", "v", int64(2), "new", true)},
			[][2]int{{1, 0}}},
		{"no identity, same count: edited in place",
			[]map[string]any{h("v", int64(1))},
			[]map[string]any{h("v", int64(2))},
			[][2]int{{0, 0}}},
		{"identity, nothing in common: all replaced",
			[]map[string]any{h("name", "a"), h("name", "b")},
			[]map[string]any{h("name", "c"), h("name", "d")},
			nil},
	}
	for _, c := range cases {
		if got := alignTables(c.ol, c.nl); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPatch_KeepsCRLF(t *testing.T) {
	orig := "a = 1\r\n\r\n[t]\r\nb = 2\r\n"
	target := retarget(t, orig, func(m map[string]any) {
		sub(m, "t")["c"] = int64(3)
		m["u"] = map[string]any{"d": int64(4)}
	})
	want := "a = 1\r\n\r\n[t]\r\nb = 2\r\nc = 3\r\n\r\n[u]\r\nd = 4\r\n"
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestPatch_NoTrailingNewline(t *testing.T) {
	orig := "a = 1 # last line has no newline"
	target := retarget(t, orig, func(m map[string]any) { m["b"] = int64(2) })
	want := "a = 1 # last line has no newline\nb = 2\n"
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestPatch_ByteOrderMark(t *testing.T) {
	orig := "\uFEFFa = 1 # c\n"
	target := retarget(t, orig, func(m map[string]any) { m["a"] = int64(2) })
	if got, want := mustPatch(t, orig, target), "\uFEFFa = 2 # c\n"; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestPatch_QuotedKeys(t *testing.T) {
	orig := "[projects.\"/Users/me/code\"] # trusted\ntrust_level = \"trusted\"\n"
	target := retarget(t, orig, func(m map[string]any) {
		sub(m, "projects")["/Users/me/new"] = map[string]any{"trust_level": "trusted"}
		sub(sub(m, "projects"), "/Users/me/code")["note key"] = "x"
	})
	want := "[projects.\"/Users/me/code\"] # trusted\ntrust_level = \"trusted\"\n\"note key\" = \"x\"\n\n[projects.\"/Users/me/new\"]\ntrust_level = \"trusted\"\n"
	if got := mustPatch(t, orig, target); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// The config package's target is a re-encoded map (alphabetical) once
// it carries unknown keys; the order hint keeps added keys in struct
// order.
func TestPatchOrdered_AddsKeysInHintOrder(t *testing.T) {
	orig := "# mine\nzeta = 1\n"
	target := []byte("alpha = 2\nmid = 3\nzeta = 1\n")
	order := []byte("zeta = 0\nmid = 0\nalpha = 0\n")
	out, err := PatchOrdered([]byte(orig), target, order)
	if err != nil {
		t.Fatal(err)
	}
	if want := "# mine\nzeta = 1\nmid = 3\nalpha = 2\n"; string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
	if _, err := PatchOrdered([]byte(orig), target, []byte("= broken")); err == nil {
		t.Error("an unparseable order hint was accepted")
	}
}

func TestPatch_TableBecomesValueAndBack(t *testing.T) {
	orig := "[x]\na = 1 # c\n[y]\nb = 2\n"
	target := retarget(t, orig, func(m map[string]any) {
		m["x"] = "flat"
		m["y"] = map[string]any{"b": int64(2), "c": []any{int64(1)}}
	})
	got := mustPatch(t, orig, target)
	if !strings.Contains(got, "[y]\nb = 2\nc = [1]\n") {
		t.Errorf("unchanged [y] not patched in place:\n%s", got)
	}
}

// TestRenderScalar_LocalTimes — BurntSushi's encoder shifts local
// (offset-less) datetimes by the machine's UTC offset; the patcher
// formats them from their wall clock so they decode back to themselves
// in any time zone.
func TestRenderScalar_LocalTimes(t *testing.T) {
	for _, lit := range []string{"1979-05-27", "07:32:00", "1979-05-27T07:32:00.5", "1979-05-27T07:32:00Z", "1979-05-27T00:32:00-07:00"} {
		m := decode(t, "v = "+lit)
		s, err := renderScalar(m["v"])
		if err != nil {
			t.Fatal(err)
		}
		if back := decode(t, "v = "+s); !Equal(back["v"], m["v"]) {
			t.Errorf("%s rendered as %s, which decodes to %v not %v", lit, s, back["v"], m["v"])
		}
	}
}

func TestEqual(t *testing.T) {
	nan := decode(t, "v = nan")["v"]
	cases := []struct {
		a, b string
		eq   bool
	}{
		{"v = nan", "v = nan", true},
		{"v = 0.0", "v = -0.0", false},
		{"v = 1", "v = 1.0", false},
		{"[[v]]\na = 1", "v = [{ a = 1 }]", true},
		{"v = 1979-05-27T07:32:00Z", "v = 1979-05-27T00:32:00-07:00", false},
		{"v = 1979-05-27T07:32:00", "v = 1979-05-27T07:32:00Z", false},
	}
	for _, c := range cases {
		if got := Equal(decode(t, c.a), decode(t, c.b)); got != c.eq {
			t.Errorf("Equal(%q, %q) = %v, want %v", c.a, c.b, got, c.eq)
		}
	}
	if !Equal(nan, nan) {
		t.Error("NaN should equal NaN")
	}
	if Equal(time.Time{}, "x") {
		t.Error("mismatched types compared equal")
	}
}

// --- fallbacks: Patch errs (and callers write the target whole) ---

func TestPatch_FallsBackOnUnparseableOriginal(t *testing.T) {
	if _, err := Patch([]byte("a = \n"), []byte("a = 1\n")); err == nil {
		t.Fatal("Patch accepted an original that doesn't parse")
	}
}

func TestPatch_FallsBackOnUnparseableTarget(t *testing.T) {
	if _, err := Patch([]byte("a = 1\n"), []byte("a = = 2\n")); err == nil {
		t.Fatal("Patch accepted a target that doesn't parse")
	}
}

// BurntSushi accepts extending an inline table with a [header], which
// the TOML spec forbids; the patcher doesn't model it and declines.
func TestPatch_FallsBackOnUnsupportedShape(t *testing.T) {
	orig := "x = { a = 1 } # inline\n[x.b]\nc = 2\n"
	target := retarget(t, orig, func(m map[string]any) { m["y"] = int64(1) })
	_, err := Patch([]byte(orig), target)
	if !errors.Is(err, errShape) {
		t.Fatalf("err = %v, want errShape", err)
	}
}

// The verification step is the safety net: whatever went wrong while
// building the patch, text that doesn't decode to the target is never
// returned.
func TestPatch_FallsBackWhenVerificationFails(t *testing.T) {
	afterApply = func(b []byte) []byte { return bytes.Replace(b, []byte("= 2"), []byte("= 3"), 1) }
	t.Cleanup(func() { afterApply = nil })
	out, err := Patch([]byte("a = 1 # c\n"), []byte("a = 2\n"))
	if err == nil {
		t.Fatalf("corrupted patch returned: %q", out)
	}
	var ve *verifyError
	if !errors.As(err, &ve) {
		t.Errorf("err = %v, want a verifyError", err)
	}
}

func TestPatch_RecoversFromPanics(t *testing.T) {
	afterApply = func([]byte) []byte { panic("boom") }
	t.Cleanup(func() { afterApply = nil })
	if _, err := Patch([]byte("a = 1\n"), []byte("a = 2\n")); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the recovered panic", err)
	}
}

func TestApply_RejectsOverlaps(t *testing.T) {
	src := []byte("abcdef")
	if _, err := apply(src, []edit{{start: 1, end: 4}, {start: 2, end: 5, seq: 1}}, "\n"); !errors.Is(err, errConflict) {
		t.Fatalf("err = %v, want errConflict", err)
	}
}
