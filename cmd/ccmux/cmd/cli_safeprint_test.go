//go:build !windows

package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// osc52 is a clipboard-write escape: printed raw, it replaces the
// user's clipboard with "Hello".
const osc52 = "\x1b]52;c;SGVsbG8=\x07"

// assertTerminalSafe fails when out carries an ESC or BEL (or any other
// C0 control besides newline and tab) — what a hostile name would use
// to drive the user's terminal.
func assertTerminalSafe(t *testing.T, what, out string) {
	t.Helper()
	for _, r := range out {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("%s printed control character %U raw:\n%q", what, r, out)
			return
		}
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Error(err)
	}
}

// TestNotesCLI_StripsTerminalEscapes — `ccmux notes list/search/read`
// printed note file names, snippets and bodies raw, so a note named
// `n<OSC 52>x.md` in a cloned repo (or served by a peer's daemon)
// rewrote the clipboard of whoever listed the vault.
func TestNotesCLI_StripsTerminalEscapes(t *testing.T) {
	e := newCLIEnv(t)
	evil := "n" + osc52 + "x.md"
	e.fakeDaemon(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/notes" && r.URL.Query().Get("file") != "":
			writeJSON(t, w, daemon.NoteContent{Rel: evil, Content: "body " + osc52 + "end\n"})
		case r.URL.Path == "/v1/notes":
			writeJSON(t, w, []daemon.NoteEntry{{Rel: "docs/" + evil, Dir: "docs" + osc52, Modified: time.Now()}})
		case r.URL.Path == "/v1/notes/search":
			writeJSON(t, w, []daemon.SearchHit{{Rel: evil, LineNum: 3, Snippet: "hit " + osc52 + "here"}})
		default:
			http.NotFound(w, r)
		}
	}))

	list := e.run("", "notes", "list", "alpha")
	if list.code != 0 {
		t.Fatalf("notes list exit %d\nstderr: %s", list.code, list.stderr)
	}
	assertTerminalSafe(t, "notes list", list.stdout)
	if !strings.Contains(list.stdout, "docs/nx.md") {
		t.Errorf("notes list should still show the (cleaned) name:\n%s", list.stdout)
	}

	search := e.run("", "notes", "search", "alpha", "hit")
	if search.code != 0 {
		t.Fatalf("notes search exit %d\nstderr: %s", search.code, search.stderr)
	}
	assertTerminalSafe(t, "notes search", search.stdout)
	if !strings.Contains(search.stdout, "nx.md:3: hit here") {
		t.Errorf("notes search should still show the hit:\n%s", search.stdout)
	}

	read := e.run("", "notes", "read", "alpha", "x.md")
	if read.code != 0 {
		t.Fatalf("notes read exit %d\nstderr: %s", read.code, read.stderr)
	}
	assertTerminalSafe(t, "notes read", read.stdout)
	if read.stdout != "body end\n" {
		t.Errorf("notes read = %q, want the body minus the escape", read.stdout)
	}
}

// TestList_StripsTerminalEscapes — a session named with an OSC title
// sequence (tmux allows it) was printed raw by `ccmux list`. The JSON
// form stays exact: the encoder escapes control characters itself.
func TestList_StripsTerminalEscapes(t *testing.T) {
	e := newCLIEnv(t)
	name := "c-evil" + "\x1b]0;pwned\x07"
	e.fakeDaemon(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, []daemon.SessionState{{Name: name, Host: "local", State: "idle", Path: "/work/" + osc52}})
	}))

	res := e.run("", "list")
	if res.code != 0 {
		t.Fatalf("list exit %d\nstderr: %s", res.code, res.stderr)
	}
	assertTerminalSafe(t, "list", res.stdout)
	if !strings.Contains(res.stdout, "c-evil") {
		t.Errorf("list should still show the session:\n%s", res.stdout)
	}

	js := e.run("", "list", "--json")
	var got []daemon.SessionState
	if err := json.Unmarshal([]byte(js.stdout), &got); err != nil || len(got) != 1 || got[0].Name != name {
		t.Errorf("list --json must keep the exact name (JSON escapes it): %v %q", err, js.stdout)
	}
}

// TestProject_StripsTerminalEscapes — `ccmux project` printed running
// session names (from tmux) raw.
func TestProject_StripsTerminalEscapes(t *testing.T) {
	e := newCLIEnv(t)
	root := e.mkdir("Projects")
	dir := e.mkdir("Projects/alpha/.git")
	dir = filepath.Dir(dir)
	e.writeExe("tmux", `case "$1" in
list-sessions)
  case "$3" in
  *session_created*) printf 'c-al\033]0;pwned\007pha\t1700000000\t1700000000\t0\t1\t`+dir+`\n' ;;
  *) printf 'c-al\033]0;pwned\007pha\t\n' ;;
  esac ;;
esac
exit 0
`)
	res := e.run("", "--projects", root, "project", "alpha")
	if res.code != 0 {
		t.Fatalf("project exit %d\nstderr: %s", res.code, res.stderr)
	}
	assertTerminalSafe(t, "project", res.stdout)
	if !strings.Contains(res.stdout, "c-alpha") {
		t.Errorf("project should still list the running session:\n%s", res.stdout)
	}
}

// TestListConversations_StripsTerminalEscapes — conversation IDs come
// from transcript file names and the fallback column from the recorded
// working directory; both were printed raw.
func TestListConversations_StripsTerminalEscapes(t *testing.T) {
	e := newCLIEnv(t)
	dir := e.mkdir(".claude/projects/-work-app")
	id := "3f2a1b4c" + osc52
	body := `{"type":"user","cwd":"/work/app\u001b]52;c;SGVsbG8=\u0007","message":{"role":"user","content":""},"timestamp":"2026-09-01T10:00:00.000Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res := e.run("", "list-conversations")
	if res.code != 0 {
		t.Fatalf("list-conversations exit %d\nstderr: %s", res.code, res.stderr)
	}
	assertTerminalSafe(t, "list-conversations", res.stdout)
	if !strings.Contains(res.stdout, "3f2a1b4c") {
		t.Errorf("list-conversations should still list the conversation:\n%s", res.stdout)
	}
}

// TestCLIErrors_StripTerminalEscapes — errors quote daemon responses;
// a hostile (or buggy) daemon's error body went to stderr raw.
func TestCLIErrors_StripTerminalEscapes(t *testing.T) {
	e := newCLIEnv(t)
	e.fakeDaemon(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such project "+osc52, http.StatusNotFound)
	}))
	res := e.run("", "notes", "list", "alpha")
	if res.code == 0 {
		t.Fatalf("notes list against a 404 should fail; stdout: %s", res.stdout)
	}
	assertTerminalSafe(t, "error output", res.stderr)
	if !strings.Contains(res.stderr, "no such project") {
		t.Errorf("error should still carry the daemon's message: %q", res.stderr)
	}
}
