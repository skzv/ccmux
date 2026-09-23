package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Unit tests for the helpers behind the CLI fixes; the end-to-end
// regressions live in cli_regressions_test.go / cli_units_test.go.

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
	for _, in := range []string{"", "d", "7", "-1d", "7dd", "7x", "1d-2h", "abc", "99999999999999d"} {
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
		{"c-foo", []string{"c-c-foo"}, "c-c-foo"},        // project literally named c-foo
		{"c-foo", []string{"c-foo", "c-c-foo"}, "c-foo"}, // a live session name wins
		{"work", []string{"work"}, "work"},               // bare (unprefixed) session
		{"web", []string{"c-web"}, "c-web"},              // project name
		{"my.app", []string{"c-my_app"}, "c-my_app"},     // sanitized like attach
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
		{false, false, "tmux attach-session -t =c-x"},
		{false, true, "tmux attach-session -d -t =c-x"},
		{true, false, "tmux switch-client -t =c-x"},
		{true, true, "tmux switch-client -t =c-x"}, // -d is meaningless for a switch
	}
	for _, tc := range cases {
		if got := strings.Join(tmuxAttachCmd("c-x", tc.detach, tc.nested).Args, " "); got != tc.want {
			t.Errorf("tmuxAttachCmd(nested=%v, detach=%v) = %q, want %q", tc.nested, tc.detach, got, tc.want)
		}
	}
}
