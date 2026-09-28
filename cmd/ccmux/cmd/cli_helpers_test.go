package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/tmux"
)

// Unit tests for the helpers behind the CLI fixes; the end-to-end
// regressions live in cli_regressions_test.go / cli_units_test.go.

// exactTarget is the exact-match tmux target the tmux package builds
// for a session name ("=name", or "=name:" which also resolves dotted
// names). Derived from tmux.AttachArgs so these tests follow the
// package instead of pinning one spelling. It lives here, untagged,
// because both this file and the !windows CLI harness use it —
// defined only in the harness, `GOOS=windows go vet` failed to
// type-check this file.
func exactTarget(name string) string {
	args := tmux.AttachArgs(name, false)
	return args[len(args)-1]
}

func TestParseSince(t *testing.T) {
	day := 24 * time.Hour
	ok := map[string]time.Duration{
		"7d":    7 * day,
		"1d12h": day + 12*time.Hour,
		"1.5d":  36 * time.Hour,
		"24h":   24 * time.Hour,
		"90m":   90 * time.Minute,
		"0d":    0,
		" 2d ":  2 * day,
	}
	for in, want := range ok {
		if got, err := parseSince(in); err != nil || got != want {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "d", "7", "-1d", "7dd", "7x", "1d-2h", "abc", "99999999999999d",
		"NaNd", "nand", "Infd", "+Infd", "-Infd", "NaNd1h"} {
		if _, err := parseSince(in); err == nil {
			t.Errorf("parseSince(%q) accepted, want an error", in)
		}
	}
}

// named is a session list of bare names, each with no known path.
func named(names ...string) []tmux.Session {
	out := make([]tmux.Session, len(names))
	for i, n := range names {
		out[i] = tmux.Session{Name: n}
	}
	return out
}

// noProjectDir is a projectDir for resolveKillTarget under which no
// project directory exists: the argument maps to a missing path.
func noProjectDir(arg string) (string, bool) { return filepath.Join("/nonexistent", arg), false }

func TestResolveKillTarget(t *testing.T) {
	cases := []struct {
		arg  string
		live []string
		want string
	}{
		{"c-foo", []string{"c-c-foo"}, "c-c-foo"},                                                  // project literally named c-foo
		{"c-foo", []string{"c-foo", "c-c-foo"}, "c-foo"},                                           // a live session name wins
		{"work", []string{"work"}, "work"},                                                         // bare (unprefixed) session
		{"web", []string{"c-web"}, "c-web"},                                                        // project name
		{"my.app", []string{tmux.SessionNameForPath("my.app")}, tmux.SessionNameForPath("my.app")}, // sanitized like attach
		{"/x/Projects/web", []string{"c-web"}, "c-web"},                                            // a path, gone: its folder's name
	}
	for _, tc := range cases {
		got, err := resolveKillTarget(tc.arg, named(tc.live...), noProjectDir)
		if err != nil || got != tc.want {
			t.Errorf("resolveKillTarget(%q, live=%v) = %q, %v; want %q", tc.arg, tc.live, got, err, tc.want)
		}
	}
	if _, err := resolveKillTarget("c-foo", nil, noProjectDir); err == nil {
		t.Error("nothing live: want an error, not a guessed target")
	}
}

// TestResolveKillTarget_ByDirectory — `ccmux kill api` killed whichever
// session was called c-api, even one running a same-named project in
// another directory. A project that exists is killed by its directory:
// its own session (plain or path-tagged), never the other project's.
func TestResolveKillTarget_ByDirectory(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mine, other := filepath.Join(base, "Projects", "api"), filepath.Join(base, "work", "api")
	for _, d := range []string{mine, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tagged := tmux.PathTaggedSessionName(mine)
	dirOf := func(string) (string, bool) { return mine, true }

	got, err := resolveKillTarget("api", []tmux.Session{{Name: "c-api", Path: other}, {Name: tagged, Path: mine}}, dirOf)
	if err != nil || got != tagged {
		t.Errorf("kill api with its tagged session running = %q, %v; want %s", got, err, tagged)
	}
	got, err = resolveKillTarget("api", []tmux.Session{{Name: "c-api", Path: mine}, {Name: "c-api-2", Path: mine}}, dirOf)
	if err != nil || got != "c-api" {
		t.Errorf("kill api with its plain session running = %q, %v; want c-api", got, err)
	}
	_, err = resolveKillTarget("api", []tmux.Session{{Name: "c-api", Path: other}}, dirOf)
	if err == nil {
		t.Fatal("only the other project's c-api runs: want an error, not a kill of it")
	}
	for _, want := range []string{mine, "c-api runs in " + other, "ccmux kill c-api"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// TestResolveKillTarget_RefusesSessionIDs — `ccmux kill '$1'` exited 0
// after killing whichever session had tmux ID $1. A "$" name is refused
// before anything is looked up, and isn't mapped as a project either.
func TestResolveKillTarget_RefusesSessionIDs(t *testing.T) {
	for _, arg := range []string{"$0", "$1", "$x"} {
		mapped := false
		got, err := resolveKillTarget(arg, named(arg, tmux.SessionNameForPath(arg)), func(a string) (string, bool) {
			mapped = true
			return noProjectDir(a)
		})
		if err == nil {
			t.Errorf("resolveKillTarget(%q) = %q, want an error", arg, got)
			continue
		}
		if !strings.Contains(err.Error(), "session ID") {
			t.Errorf("resolveKillTarget(%q) error %q should say tmux reads it as a session ID", arg, err)
		}
		if mapped {
			t.Errorf("resolveKillTarget(%q) mapped it as a project", arg)
		}
		if err := refuseSessionID("kill", arg); err == nil || !strings.Contains(err.Error(), "session ID") {
			t.Errorf("refuseSessionID(kill, %q) = %v, want a session-ID refusal", arg, err)
		}
	}
	if err := refuseSessionID("kill", "c-ok"); err != nil {
		t.Errorf("refuseSessionID(kill, c-ok) = %v", err)
	}
}

// TestResolveKillTarget_InvalidTargetsOnlyMapAsProjects — an argument a
// tmux target can't carry (a dotted project name, a path) is never
// taken as a session name itself, only mapped as a project.
func TestResolveKillTarget_InvalidTargetsOnlyMapAsProjects(t *testing.T) {
	for _, arg := range []string{"my.app", "/x/Projects/web", "$x/web", "a:b"} {
		mapped := tmux.SessionNameForPath(arg)
		got, err := resolveKillTarget(arg, named(arg, mapped), noProjectDir)
		if err != nil || got != mapped {
			t.Errorf("resolveKillTarget(%q) = %q, %v; want %q", arg, got, err, mapped)
		}
	}
}

func TestResolveAttachDir(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "Projects")
	cwd := filepath.Join(base, "cwd")
	for _, d := range []string{filepath.Join(root, "api"), filepath.Join(root, "both"), filepath.Join(cwd, "both"), filepath.Join(cwd, "local")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(cwd)

	cases := []struct {
		arg       string
		wantDir   string
		wantFound bool
	}{
		{"api", filepath.Join(root, "api"), true},    // project under the root, from anywhere
		{"both", filepath.Join(root, "both"), true},  // a bare name prefers the project
		{"./both", filepath.Join(cwd, "both"), true}, // a path stays a path
		{"local", filepath.Join(cwd, "local"), true}, // no such project: CWD fallback
		{"", cwd, true}, // no argument: CWD
		{"nope", filepath.Join(cwd, "nope"), false},         // missing: not found
		{"../cwd/local", filepath.Join(cwd, "local"), true}, // relative path
	}
	for _, tc := range cases {
		dir, found := resolveAttachDir(tc.arg, root)
		if dir != tc.wantDir || found != tc.wantFound {
			t.Errorf("resolveAttachDir(%q) = %q, %v; want %q, %v", tc.arg, dir, found, tc.wantDir, tc.wantFound)
		}
	}
}

func TestRunDoctorBinChecks_OptionalNeverCounts(t *testing.T) {
	missing := map[string]bool{"rg": true, "mosh": true}
	lookPath := func(bin string) (string, error) {
		if missing[bin] {
			return "", errors.New("not found")
		}
		return "/bin/" + bin, nil
	}
	var out bytes.Buffer
	bad := runDoctorBinChecks(&out, []doctorBinCheck{
		{bin: "tmux", hint: "h"},
		{bin: "mosh", hint: "h"},
		{bin: "rg", hint: "h", optional: true},
	}, lookPath)
	if bad != 1 {
		t.Errorf("bad = %d, want 1 (mosh only; rg is optional)\n%s", bad, out.String())
	}
	if !strings.Contains(out.String(), "rg not on PATH (optional)") {
		t.Errorf("missing optional rg should still be reported:\n%s", out.String())
	}
}

func TestTmuxAttachCmd_Selection(t *testing.T) {
	cases := []struct {
		nested, detach bool
		want           string
	}{
		{false, false, "tmux attach-session -t " + exactTarget("c-x")},
		{false, true, "tmux attach-session -d -t " + exactTarget("c-x")},
		{true, false, "tmux switch-client -t " + exactTarget("c-x")},
		{true, true, "tmux switch-client -t " + exactTarget("c-x")}, // -d is meaningless for a switch
	}
	for _, tc := range cases {
		if got := strings.Join(tmuxAttachCmd("c-x", tc.detach, tc.nested).Args, " "); got != tc.want {
			t.Errorf("tmuxAttachCmd(nested=%v, detach=%v) = %q, want %q", tc.nested, tc.detach, got, tc.want)
		}
	}
}

func TestSafeField(t *testing.T) {
	cases := map[string]string{
		"plain.md":                    "plain.md",
		"n\x1b]52;c;SGVsbG8=\x07x.md": "nx.md",        // OSC 52 clipboard write
		"c-\x1b[2J\x1b[Hwipe":         "c-wipe",       // CSI clear screen
		"a\tb\nc":                     "a b c",        // row/column breakers
		"café → 漢字.md":                "café → 漢字.md", // real text survives
	}
	for in, want := range cases {
		if got := safeField(in); got != want {
			t.Errorf("safeField(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ErrorMessage(errors.New("line one\nline \x1b]0;t\x07two")); got != "line one\nline two" {
		t.Errorf("ErrorMessage kept an escape or lost a newline: %q", got)
	}
}
