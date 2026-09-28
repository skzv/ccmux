package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// cliCommandWords returns the names of ccmux's CLI (sub)commands, read
// from the Cobra `Use:` fields under cmd/ccmux/cmd, so the list below
// can't drift from the real command set.
func cliCommandWords(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("../../cmd/ccmux/cmd/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no CLI sources found: %v", err)
	}
	use := regexp.MustCompile(`Use:\s*"([a-z][a-z0-9-]*)`)
	words := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range use.FindAllStringSubmatch(string(b), -1) {
			words[m[1]] = true
		}
	}
	if !words["host"] || !words["add"] || !words["update"] {
		t.Fatalf("CLI command scan looks wrong: %v", words)
	}
	return words
}

// commandsIn returns the literal commands a catalog key mentions:
// `ccmux <subcommand> [<subcommand>]` runs (only words that are real
// CLI commands count, so "ccmux is …" is prose), the binaries ccmuxd /
// ccmux-mcp, and every backtick-quoted span with a space in it
// (`ssh -t <host>`).
func commandsIn(key string, words map[string]bool) []string {
	var out []string
	run := regexp.MustCompile(`\bccmux((?: [a-z][a-z0-9-]*)+)`)
	for _, m := range run.FindAllStringSubmatch(key, -1) {
		cmd := "ccmux"
		for _, w := range strings.Fields(m[1]) {
			if !words[w] {
				break
			}
			cmd += " " + w
		}
		if cmd != "ccmux" {
			out = append(out, cmd)
		}
	}
	out = append(out, regexp.MustCompile(`\bccmux(?:d|-mcp)\b`).FindAllString(key, -1)...)
	for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(key, -1) {
		if strings.Contains(m[1], " ") {
			out = append(out, m[1])
		}
	}
	return out
}

// TestCatalogs_KeepCommandsVerbatim — commands are what the user
// types, so they must never be translated: the tour told German users
// to run "ccmux-Host hinzufügen …" and Spanish users "Agregar host
// ccmux…" for `ccmux host add`. Every command a key mentions must
// appear unchanged in each locale's translation of it.
func TestCatalogs_KeepCommandsVerbatim(t *testing.T) {
	words := cliCommandWords(t)
	for _, language := range Languages() {
		if language.Code == LangEn {
			continue
		}
		t.Run(string(language.Code), func(t *testing.T) {
			table := map[string]string{}
			b, err := localesFS.ReadFile("locales/" + string(language.Code) + ".toml")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tomlDecode(string(b), &table); err != nil {
				t.Fatal(err)
			}
			keys := make([]string, 0, len(table))
			for k := range table {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, key := range keys {
				for _, cmd := range commandsIn(key, words) {
					if !containsCommand(table[key], cmd) {
						t.Errorf("command %q translated away in %q => %q", cmd, key, table[key])
					}
				}
			}
		})
	}
}

// keyLabels are the accepted names of a key in a translated key hint:
// the key's own label, or the label a local keyboard prints on it
// (French "Entrée"/"Échap", Spanish "Intro"), never a word that means
// something else — "tab:" came out as a browser tab ("Registerkarte",
// "pestaña", "onglet", "вкладка") and "enter:" as the verb ("ingresar",
// "eingeben", "saisir").
var keyLabels = map[string][]string{
	"tab":   {"tab"},
	"enter": {"enter", "entrée", "intro", "eingabetaste", "ввод", "엔터"},
	"esc":   {"esc", "échap"},
}

// TestCatalogs_KeyHintsNameTheKeys — in a hint like "tab: next field
// enter: create", every key named before a colon must still be named in
// the translation (see keyLabels).
func TestCatalogs_KeyHintsNameTheKeys(t *testing.T) {
	hint := regexp.MustCompile(`(?i)\b(tab|enter|esc)\s*:`)
	for _, language := range Languages() {
		if language.Code == LangEn {
			continue
		}
		t.Run(string(language.Code), func(t *testing.T) {
			table := map[string]string{}
			b, err := localesFS.ReadFile("locales/" + string(language.Code) + ".toml")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tomlDecode(string(b), &table); err != nil {
				t.Fatal(err)
			}
			for key, value := range table {
				lower := strings.ToLower(value)
				for _, m := range hint.FindAllStringSubmatch(key, -1) {
					named := false
					for _, label := range keyLabels[strings.ToLower(m[1])] {
						if strings.Contains(lower, label) {
							named = true
						}
					}
					if !named {
						t.Errorf("key %q not named in %q => %q", m[1], key, value)
					}
				}
			}
		})
	}
}

// containsCommand reports whether translation carries cmd verbatim. A
// <placeholder> in the command (`ssh -t <host>`) names what the user
// fills in, so it may be translated; everything else must match.
func containsCommand(translation, cmd string) bool {
	parts := regexp.MustCompile(`<[^>]*>`).Split(cmd, -1)
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile(strings.Join(parts, `<[^>]*>`)).MatchString(translation)
}

// TestCommandsIn pins what counts as a command in a catalog key.
func TestCommandsIn(t *testing.T) {
	words := map[string]bool{"host": true, "add": true, "update": true, "moshi-setup": true}
	cases := map[string][]string{
		"  ccmux host add …    — supervise sessions":   {"ccmux host add"},
		"[↑ ccmux update — %s behind %s]":              {"ccmux update"},
		"ccmux is running in this session":             nil,
		"ccmuxd polls tmux":                            {"ccmuxd"},
		"plain `ssh -t <host>` into the selected peer": {"ssh -t <host>"},
		"re-opens any time with `T`.":                  nil,
	}
	for key, want := range cases {
		got := commandsIn(key, words)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("commandsIn(%q) = %q, want %q", key, got, want)
		}
	}
}
