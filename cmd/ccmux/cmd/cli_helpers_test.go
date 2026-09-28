package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
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

func TestResolveKillTarget(t *testing.T) {
	sessionsHas := func(live ...string) func(context.Context, string) (bool, error) {
		return func(_ context.Context, name string) (bool, error) {
			for _, s := range live {
				if s == name {
					return true, nil
				}
			}
			return false, nil
		}
	}
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
	}
	for _, tc := range cases {
		got, err := resolveKillTarget(context.Background(), tc.arg, sessionsHas(tc.live...))
		if err != nil || got != tc.want {
			t.Errorf("resolveKillTarget(%q, live=%v) = %q, %v; want %q", tc.arg, tc.live, got, err, tc.want)
		}
	}
	if _, err := resolveKillTarget(context.Background(), "c-foo", sessionsHas()); err == nil {
		t.Error("nothing live: want an error, not a guessed target")
	}
	boom := errors.New("tmux missing")
	if _, err := resolveKillTarget(context.Background(), "x", func(context.Context, string) (bool, error) { return false, boom }); !errors.Is(err, boom) {
		t.Errorf("has() error must propagate, got %v", err)
	}
}

// tmuxLikeHas is a `has` for resolveKillTarget that behaves like real
// tmux's `has-session -t =name:`: a name starting with "$" followed by
// a live session ID resolves as that ID, whatever the session is
// called. calls records every name it was asked about.
func tmuxLikeHas(names []string, ids int, calls *[]string) func(context.Context, string) (bool, error) {
	return func(_ context.Context, name string) (bool, error) {
		*calls = append(*calls, name)
		if n, err := strconv.Atoi(strings.TrimPrefix(name, "$")); err == nil && strings.HasPrefix(name, "$") {
			return n >= 0 && n < ids, nil
		}
		return slices.Contains(names, name), nil
	}
}

// TestResolveKillTarget_RefusesSessionIDs — `ccmux kill '$1'` exited 0
// after killing whichever session had tmux ID $1. A "$" name is refused
// before tmux is asked anything, and isn't mapped as a project either.
func TestResolveKillTarget_RefusesSessionIDs(t *testing.T) {
	for _, arg := range []string{"$0", "$1", "$x"} {
		var calls []string
		got, err := resolveKillTarget(context.Background(), arg, tmuxLikeHas([]string{"a", "b"}, 2, &calls))
		if err == nil {
			t.Errorf("resolveKillTarget(%q) = %q, want an error", arg, got)
			continue
		}
		if !strings.Contains(err.Error(), "session ID") {
			t.Errorf("resolveKillTarget(%q) error %q should say tmux reads it as a session ID", arg, err)
		}
		if len(calls) != 0 {
			t.Errorf("resolveKillTarget(%q) asked tmux about %v; a session-ID name must not reach tmux", arg, calls)
		}
	}
}

// TestResolveKillTarget_InvalidTargetsOnlyMapAsProjects — an argument a
// tmux target can't carry (a dotted project name, a path) is never
// looked up as a session itself, only mapped through SessionNameForPath.
func TestResolveKillTarget_InvalidTargetsOnlyMapAsProjects(t *testing.T) {
	for _, arg := range []string{"my.app", "/x/Projects/web", "$x/web", "a:b"} {
		mapped := tmux.SessionNameForPath(arg)
		var calls []string
		got, err := resolveKillTarget(context.Background(), arg, tmuxLikeHas([]string{mapped}, 0, &calls))
		if err != nil || got != mapped {
			t.Errorf("resolveKillTarget(%q) = %q, %v; want %q", arg, got, err, mapped)
		}
		if !reflect.DeepEqual(calls, []string{mapped}) {
			t.Errorf("resolveKillTarget(%q) looked up %v; want only the project's session %q", arg, calls, mapped)
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
