package codexconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/skzv/ccmux/internal/tomlpatch"
)

// handEdited is a ~/.codex/config.toml the way people actually keep
// one: commented, grouped, with a trailing note on half the lines.
const handEdited = `# Codex, tuned by hand. ccmux flips effort/YOLO from the Agents tab.
model = "gpt-5-codex"              # pinned until the next eval
model_reasoning_effort = "medium"

notify = ["terminal-notifier", "-title", "codex"]

# Trusted checkouts
[projects."/Users/me/code/ccmux"]
trust_level = "trusted"

[mcp_servers.docs]
command = "npx"
args = ["-y", "docs-mcp@latest"]   # pin me eventually

[profiles.fast]
model = "gpt-5-mini"
model_reasoning_effort = "low"
`

func writeCodexConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(withFakeCodexDir(t), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readText(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSetEffortLevel_KeepsCommentsAndLayout — the Codex tab's effort
// toggle changes exactly one value; every comment, blank line and table
// in the user's file stays put. (WriteSettings used to re-encode the
// whole file from a map: comments gone, keys sorted.)
func TestSetEffortLevel_KeepsCommentsAndLayout(t *testing.T) {
	p := writeCodexConfig(t, handEdited)
	if _, err := SetEffortLevel("high"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(handEdited, `model_reasoning_effort = "medium"`, `model_reasoning_effort = "high"`, 1)
	if got := readText(t, p); got != want {
		t.Errorf("effort change rewrote more than one value:\n--- got\n%s\n--- want\n%s", got, want)
	}

	// Clearing the override drops just that line.
	if _, err := SetEffortLevel(""); err != nil {
		t.Fatal(err)
	}
	want = strings.Replace(handEdited, "model_reasoning_effort = \"medium\"\n", "", 1)
	if got := readText(t, p); got != want {
		t.Errorf("clearing effort rewrote more than its line:\n--- got\n%s\n--- want\n%s", got, want)
	}
}

// TestSetYoloMode_RoundTripRestoresFile — YOLO on adds its two keys
// next to the other top-level settings; YOLO off removes them again,
// leaving the file byte-for-byte as the user wrote it.
func TestSetYoloMode_RoundTripRestoresFile(t *testing.T) {
	p := writeCodexConfig(t, handEdited)
	if _, err := SetYoloMode(true); err != nil {
		t.Fatal(err)
	}
	on := readText(t, p)
	wantOn := strings.Replace(handEdited,
		"notify = [\"terminal-notifier\", \"-title\", \"codex\"]\n",
		"notify = [\"terminal-notifier\", \"-title\", \"codex\"]\napproval_policy = \"never\"\nsandbox_mode = \"danger-full-access\"\n", 1)
	if on != wantOn {
		t.Errorf("YOLO on:\n--- got\n%s\n--- want\n%s", on, wantOn)
	}
	if _, err := SetYoloMode(false); err != nil {
		t.Fatal(err)
	}
	if off := readText(t, p); off != handEdited {
		t.Errorf("YOLO off didn't restore the original file:\n--- got\n%s\n--- want\n%s", off, handEdited)
	}
}

// TestWriteSettings_FallsBackToFullRewrite — if the file can't be
// patched, the full encoding is written, as before: same data, just no
// formatting.
func TestWriteSettings_FallsBackToFullRewrite(t *testing.T) {
	p := writeCodexConfig(t, handEdited)
	patchTOML = func([]byte, []byte) ([]byte, error) { return nil, errors.New("cannot patch") }
	t.Cleanup(func() { patchTOML = tomlpatch.Patch })
	if _, err := SetEffortLevel("high"); err != nil {
		t.Fatal(err)
	}
	got := readText(t, p)
	if strings.Contains(got, "#") {
		t.Errorf("expected the comment-free full encoding:\n%s", got)
	}
	var m map[string]any
	if _, err := toml.Decode(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["model_reasoning_effort"] != "high" || m["model"] != "gpt-5-codex" {
		t.Errorf("fallback lost data: %v", m)
	}
	if _, ok := m["mcp_servers"].(map[string]any)["docs"]; !ok {
		t.Errorf("fallback lost mcp_servers.docs: %v", m)
	}
}
