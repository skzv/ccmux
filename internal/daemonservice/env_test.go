package daemonservice

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeEnv is a getenv over a fixed map, so tests never depend on the
// developer's real CODEX_HOME & co.
func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// secretEnv holds credentials and unrelated variables that a careless
// "copy the whole environment" would leak into a world-readable plist
// or unit. None of them may ever reach the service definition.
var secretEnv = map[string]string{
	"ANTHROPIC_API_KEY":    "sk-ant-secret-anthropic",
	"ANTHROPIC_AUTH_TOKEN": "secret-auth-token",
	"OPENROUTER_API_KEY":   "sk-or-secret-openrouter",
	"OPENAI_API_KEY":       "sk-secret-openai",
	"GITHUB_TOKEN":         "ghp_secret_github",
	"XDG_CONFIG_HOME":      "/unrelated/xdg-config",
	"SHELL":                "/unrelated/shell",
	"TMUX":                 "/unrelated/tmux,1,0",
}

// trickyAgentHomes sets every carried-over variable to a value that
// needs escaping in a plist (&, <, >, quotes), a systemd unit (%,
// specifier-lookalikes, backslash, quotes) or both (spaces, '=').
var trickyAgentHomes = map[string]string{
	"CLAUDE_CONFIG_DIR": "/home/me/claude config",
	"CODEX_HOME":        "/home/me/100%/%h/codex",
	"CURSOR_HOME":       `/home/me/"quoted" it's`,
	"ANTIGRAVITY_HOME":  `/home/me/back\slash/$HOME/a=b`,
}

func merged(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// TestPassthroughEnvNames_AreTheAgentHomesOnly locks the carried-over
// set. Adding a variable must be a deliberate edit here too, and no
// name that smells like a credential may ever slip in.
func TestPassthroughEnvNames_AreTheAgentHomesOnly(t *testing.T) {
	want := []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "CURSOR_HOME", "ANTIGRAVITY_HOME"}
	if got := PassthroughEnvNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("PassthroughEnvNames() = %v, want %v", got, want)
	}
	for _, name := range passthroughEnvNames {
		for _, bad := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "AUTH"} {
			if strings.Contains(name, bad) {
				t.Errorf("%s looks like a credential; it must not be written into the service definition", name)
			}
		}
	}
	// The exported copy must not alias the package list.
	PassthroughEnvNames()[0] = "MUTATED"
	if passthroughEnvNames[0] != want[0] {
		t.Fatal("PassthroughEnvNames returned the package slice, not a copy")
	}
}

func TestPassthroughEnv_OnlySetAndNonEmpty(t *testing.T) {
	got := passthroughEnv(fakeEnv(merged(secretEnv, map[string]string{
		"CODEX_HOME":        "/srv/codex",
		"CLAUDE_CONFIG_DIR": "", // set but empty: the readers treat this as unset
		"ANTIGRAVITY_HOME":  "/srv/agy",
	})))
	want := []EnvVar{{"CODEX_HOME", "/srv/codex"}, {"ANTIGRAVITY_HOME", "/srv/agy"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("passthroughEnv = %#v, want %#v", got, want)
	}
	if got := passthroughEnv(fakeEnv(secretEnv)); len(got) != 0 {
		t.Fatalf("passthroughEnv with no agent homes set = %#v, want none", got)
	}
}

func renderPlist(t *testing.T, env []EnvVar) string {
	t.Helper()
	var sb strings.Builder
	if err := plistTemplate.Execute(&sb, plistData{
		Label:      Label,
		Binary:     "/opt/homebrew/bin/ccmuxd",
		StdoutPath: "/Users/me/.local/state/ccmux/ccmuxd.stdout.log",
		StderrPath: "/Users/me/.local/state/ccmux/ccmuxd.stderr.log",
		HomeDir:    "/Users/me",
		WorkingDir: "/Users/me",
		Path:       "/Users/me/.local/bin:/opt/homebrew/bin:/usr/bin:/bin",
		Env:        env,
	}); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// plistEnvDict parses the rendered plist and returns its
// EnvironmentVariables dict, failing on malformed XML.
func plistEnvDict(t *testing.T, body string) map[string]string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(body))
	dec.Strict = true
	var (
		path    []string
		lastKey string
		text    strings.Builder
		inEnv   bool
		out     = map[string]string{}
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("rendered plist is not well-formed XML: %v\n%s", err, body)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			path = append(path, el.Name.Local)
			text.Reset()
			if el.Name.Local == "dict" && len(path) == 3 && lastKey == "EnvironmentVariables" {
				inEnv = true
			}
		case xml.CharData:
			text.Write(el)
		case xml.EndElement:
			switch {
			case el.Name.Local == "key":
				lastKey = text.String()
			case el.Name.Local == "string" && inEnv:
				out[lastKey] = text.String()
			case el.Name.Local == "dict" && inEnv && len(path) == 3:
				inEnv = false
			}
			path = path[:len(path)-1]
		}
	}
	return out
}

// TestPlistTemplate_AgentHomeOverrides renders the LaunchAgent with and
// without each carried-over variable: a set variable lands in
// EnvironmentVariables with its exact value (XML-escaped on the wire),
// an unset one is absent, and HOME/PATH are always there.
func TestPlistTemplate_AgentHomeOverrides(t *testing.T) {
	const value = "/Users/a&b/<x> dir"
	base := map[string]string{"HOME": "/Users/me", "PATH": "/Users/me/.local/bin:/opt/homebrew/bin:/usr/bin:/bin"}

	t.Run("none", func(t *testing.T) {
		body := renderPlist(t, passthroughEnv(fakeEnv(secretEnv)))
		if got := plistEnvDict(t, body); !reflect.DeepEqual(got, base) {
			t.Fatalf("EnvironmentVariables = %v, want only HOME and PATH %v", got, base)
		}
	})
	for _, name := range passthroughEnvNames {
		t.Run(name, func(t *testing.T) {
			body := renderPlist(t, passthroughEnv(fakeEnv(merged(secretEnv, map[string]string{name: value}))))
			want := merged(base, map[string]string{name: value})
			if got := plistEnvDict(t, body); !reflect.DeepEqual(got, want) {
				t.Fatalf("EnvironmentVariables = %v, want %v", got, want)
			}
			wire := "    <key>" + name + "</key>\n    <string>/Users/a&amp;b/&lt;x&gt; dir</string>\n  </dict>"
			if !strings.Contains(body, wire) {
				t.Errorf("plist missing escaped %s entry %q\n%s", name, wire, body)
			}
		})
	}
	t.Run("all", func(t *testing.T) {
		body := renderPlist(t, passthroughEnv(fakeEnv(merged(secretEnv, trickyAgentHomes))))
		if got, want := plistEnvDict(t, body), merged(base, trickyAgentHomes); !reflect.DeepEqual(got, want) {
			t.Fatalf("EnvironmentVariables = %v, want %v", got, want)
		}
		for name, secret := range secretEnv {
			if strings.Contains(body, name) || strings.Contains(body, secret) {
				t.Errorf("plist leaked %s:\n%s", name, body)
			}
		}
	})
}

// TestPlistTemplate_PlutilAcceptsAgentHomes hands a plist carrying every
// override (with &, <, quotes and spaces) to Apple's own parser: it must
// lint clean and read each value back byte for byte.
func TestPlistTemplate_PlutilAcceptsAgentHomes(t *testing.T) {
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("plutil not available (macOS only)")
	}
	env := merged(trickyAgentHomes, map[string]string{"CODEX_HOME": "/Users/a&b/<x> 100% dir"})
	path := filepath.Join(t.TempDir(), Label+".plist")
	if err := os.WriteFile(path, []byte(renderPlist(t, passthroughEnv(fakeEnv(env)))), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, plutil, "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v\n%s", err, out)
	}
	for name, want := range env {
		out, err := exec.CommandContext(ctx, plutil, "-extract", "EnvironmentVariables."+name, "raw", "-o", "-", path).Output()
		if err != nil {
			t.Fatalf("plutil -extract %s: %v", name, err)
		}
		if got := strings.TrimSuffix(string(out), "\n"); got != want {
			t.Errorf("plutil read %s = %q, want %q", name, got, want)
		}
	}
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestUnitFile_Golden pins the exact unit text. The with-env golden is
// hand-written to systemd's rules (systemd.exec(5) Environment=,
// systemd.syntax(7) quoting, systemd.unit(5) specifiers): each
// assignment double-quoted, \ and " backslash-escaped, % doubled, $
// left alone.
func TestUnitFile_Golden(t *testing.T) {
	if got, want := unitFile("/usr/local/bin/ccmuxd", "%h/.local/bin:/usr/local/bin:/usr/bin:/bin", nil), readGolden(t, "ccmuxd.service.golden"); got != want {
		t.Errorf("unit without overrides drifted\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	env := passthroughEnv(fakeEnv(merged(secretEnv, trickyAgentHomes)))
	got := unitFile("/usr/local/bin/ccmuxd", "/home/me/.local/bin:/usr/local/bin:/usr/bin:/bin", env)
	if want := readGolden(t, "ccmuxd_env.service.golden"); got != want {
		t.Errorf("unit with overrides drifted\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	for name, secret := range secretEnv {
		if strings.Contains(got, name) || strings.Contains(got, secret) {
			t.Errorf("unit leaked %s:\n%s", name, got)
		}
	}
}

// TestUnitFile_UsesProcessEnv covers the exported UnitFile that
// `ccmux daemon unit` prints: it carries the overrides set in this
// process and nothing else.
func TestUnitFile_UsesProcessEnv(t *testing.T) {
	for _, name := range passthroughEnvNames {
		t.Setenv(name, "")
	}
	for name, v := range secretEnv {
		t.Setenv(name, v)
	}
	t.Setenv("CODEX_HOME", "/srv/codex home")
	body := UnitFile("/usr/local/bin/ccmuxd")
	if !strings.Contains(body, "\nEnvironment=\"CODEX_HOME=/srv/codex home\"\n") {
		t.Errorf("UnitFile missing CODEX_HOME:\n%s", body)
	}
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CURSOR_HOME", "ANTIGRAVITY_HOME"} {
		if strings.Contains(body, name) {
			t.Errorf("UnitFile carried unset %s:\n%s", name, body)
		}
	}
	for name, secret := range secretEnv {
		if strings.Contains(body, name) || strings.Contains(body, secret) {
			t.Errorf("UnitFile leaked %s:\n%s", name, body)
		}
	}
}

// systemdUnquoteEnv undoes one Environment= word the way systemd does:
// strip the surrounding double quotes, apply the C-style escapes from
// systemd.syntax(7), then resolve %% specifiers. Any other specifier
// or a bare quote mid-word is an error — systemd would expand or split
// it, so the daemon would not receive the value we meant.
func systemdUnquoteEnv(word string) (string, error) {
	if len(word) < 2 || word[0] != '"' || word[len(word)-1] != '"' {
		return "", fmt.Errorf("not a double-quoted word: %q", word)
	}
	in := word[1 : len(word)-1]
	var unq strings.Builder
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c == '"' {
			return "", fmt.Errorf("unescaped quote at %d in %q", i, word)
		}
		if c != '\\' {
			unq.WriteByte(c)
			continue
		}
		i++
		if i >= len(in) {
			return "", fmt.Errorf("trailing backslash in %q", word)
		}
		switch e := in[i]; e {
		case '\\', '"', '\'':
			unq.WriteByte(e)
		case 'a':
			unq.WriteByte('\a')
		case 'b':
			unq.WriteByte('\b')
		case 'f':
			unq.WriteByte('\f')
		case 'n':
			unq.WriteByte('\n')
		case 'r':
			unq.WriteByte('\r')
		case 't':
			unq.WriteByte('\t')
		case 'v':
			unq.WriteByte('\v')
		case 's':
			unq.WriteByte(' ')
		case 'x':
			if i+2 >= len(in) {
				return "", fmt.Errorf("short \\x escape in %q", word)
			}
			n, err := strconv.ParseUint(in[i+1:i+3], 16, 8)
			if err != nil || n == 0 {
				return "", fmt.Errorf("bad \\x escape in %q", word)
			}
			unq.WriteByte(byte(n))
			i += 2
		default:
			return "", fmt.Errorf("unknown escape \\%c in %q", e, word)
		}
	}
	s := unq.String()
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			out.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '%' {
			out.WriteByte('%')
			i++
			continue
		}
		return "", fmt.Errorf("unescaped specifier at %d in %q", i, word)
	}
	return out.String(), nil
}

func TestSystemdEnvAssignment(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"/home/me/.codex", `"CODEX_HOME=/home/me/.codex"`},
		{"/home/me/my codex", `"CODEX_HOME=/home/me/my codex"`},
		{"/home/me/100%", `"CODEX_HOME=/home/me/100%%"`},
		{"/home/%u/%h", `"CODEX_HOME=/home/%%u/%%h"`},
		{`/home/me/"q"`, `"CODEX_HOME=/home/me/\"q\""`},
		{`/home/me/it's`, `"CODEX_HOME=/home/me/it's"`},
		{`C:\codex\`, `"CODEX_HOME=C:\\codex\\"`},
		{"/home/$USER/${X}", `"CODEX_HOME=/home/$USER/${X}"`},
		{"/a=b", `"CODEX_HOME=/a=b"`},
		{"/tab\there\nnl", `"CODEX_HOME=/tab\x09here\x0anl"`},
		{"/ünïcødé/コード", `"CODEX_HOME=/ünïcødé/コード"`},
	} {
		got := systemdEnvAssignment("CODEX_HOME", tc.value)
		if got != tc.want {
			t.Errorf("systemdEnvAssignment(%q) = %s, want %s", tc.value, got, tc.want)
		}
		if strings.ContainsAny(got, "\n\r") {
			t.Errorf("systemdEnvAssignment(%q) spans lines: %q", tc.value, got)
		}
		back, err := systemdUnquoteEnv(got)
		if err != nil {
			t.Errorf("systemd would not read back %s: %v", got, err)
		} else if back != "CODEX_HOME="+tc.value {
			t.Errorf("round trip of %q = %q", tc.value, back)
		}
	}
}

// TestSystemdEnvAssignment_RoundTripsRandomValues throws random bytes
// (minus NUL, which no env value can hold) at the escaper and checks
// systemd's unquoting gives each one back unchanged.
func TestSystemdEnvAssignment_RoundTripsRandomValues(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []byte("abc /._-=%$\\\"'#;\t\n\r\x01\x1b\x7f\xc3\xa9")
	for i := 0; i < 2000; i++ {
		b := make([]byte, rng.IntN(24))
		for j := range b {
			b[j] = alphabet[rng.IntN(len(alphabet))]
		}
		v := string(b)
		got := systemdEnvAssignment("CODEX_HOME", v)
		if strings.ContainsAny(got, "\n\r") {
			t.Fatalf("value %q rendered across lines: %q", v, got)
		}
		back, err := systemdUnquoteEnv(got)
		if err != nil {
			t.Fatalf("value %q: %v", v, err)
		}
		if back != "CODEX_HOME="+v {
			t.Fatalf("value %q round-tripped to %q", v, back)
		}
	}
}

// fakeRunner records service-manager calls instead of running them.
type fakeRunner struct {
	calls  [][]string
	failOn string // fail the call whose args contain this word
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	for _, a := range args {
		if f.failOn != "" && a == f.failOn {
			return []byte("simulated " + f.failOn + " output"), errors.New("exit status 1")
		}
	}
	return nil, nil
}

func testInstaller(home string, env map[string]string, r *fakeRunner) installer {
	return installer{
		home:   home,
		binary: "/opt/homebrew/bin/ccmuxd",
		getenv: fakeEnv(env),
		run:    r.run,
	}
}

// TestInstallerDarwin_ReinstallUpdatesPlist — re-running install must
// rewrite an existing plist (so a newly exported CODEX_HOME reaches the
// daemon) and bounce it through launchctl unload/load. Runs against a
// temp home and a fake launchctl.
func TestInstallerDarwin_ReinstallUpdatesPlist(t *testing.T) {
	home := t.TempDir()
	plist := darwinPlistPath(home)
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plist, []byte("stale plist from an older ccmux"), 0o644); err != nil {
		t.Fatal(err)
	}

	steps := []map[string]string{
		{"CODEX_HOME": "/srv/codex-a"},
		{"CODEX_HOME": "/srv/codex-b", "CURSOR_HOME": "/srv/cursor"},
		{},
	}
	for i, env := range steps {
		r := &fakeRunner{}
		if err := testInstaller(home, merged(secretEnv, env), r).installDarwin(); err != nil {
			t.Fatalf("install #%d: %v", i+1, err)
		}
		body, err := os.ReadFile(plist)
		if err != nil {
			t.Fatal(err)
		}
		got := plistEnvDict(t, string(body))
		for _, name := range passthroughEnvNames {
			if got[name] != env[name] {
				t.Errorf("install #%d: plist %s = %q, want %q", i+1, name, got[name], env[name])
			}
		}
		if got["HOME"] != home {
			t.Errorf("install #%d: plist HOME = %q, want %q", i+1, got["HOME"], home)
		}
		for name, secret := range secretEnv {
			if strings.Contains(string(body), secret) {
				t.Errorf("install #%d: plist leaked %s", i+1, name)
			}
		}
		want := [][]string{
			{"launchctl", "unload", "-w", plist},
			{"launchctl", "load", "-w", plist},
		}
		if !reflect.DeepEqual(r.calls, want) {
			t.Errorf("install #%d: launchctl calls = %v, want %v", i+1, r.calls, want)
		}
	}
}

func TestInstallerDarwin_LoadFailureSurfacesOutput(t *testing.T) {
	r := &fakeRunner{failOn: "load"}
	err := testInstaller(t.TempDir(), nil, r).installDarwin()
	if err == nil || !strings.Contains(err.Error(), "simulated load output") {
		t.Fatalf("installDarwin error = %v, want launchctl load output", err)
	}
}

// TestInstallerLinux_ReinstallUpdatesUnitAndRestarts — the Linux twin:
// the unit file is rewritten with the current overrides, and the daemon
// is restarted (not just `enable --now`, which leaves an already
// running daemon on its old environment).
func TestInstallerLinux_ReinstallUpdatesUnitAndRestarts(t *testing.T) {
	home := t.TempDir()
	unit := linuxUnitPath(home)
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Service]\nExecStart=/old/ccmuxd\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	steps := []map[string]string{
		{"CODEX_HOME": "/srv/codex a"},
		{"CODEX_HOME": "/srv/codex-b", "CLAUDE_CONFIG_DIR": "/srv/claude"},
		{},
	}
	for i, env := range steps {
		r := &fakeRunner{}
		if err := testInstaller(home, merged(secretEnv, env), r).installLinux(); err != nil {
			t.Fatalf("install #%d: %v", i+1, err)
		}
		b, err := os.ReadFile(unit)
		if err != nil {
			t.Fatal(err)
		}
		body := string(b)
		if strings.Contains(body, "/old/ccmuxd") {
			t.Errorf("install #%d: stale unit not replaced:\n%s", i+1, body)
		}
		for _, name := range passthroughEnvNames {
			line := "Environment=" + systemdEnvAssignment(name, env[name]) + "\n"
			if has := strings.Contains(body, line); has != (env[name] != "") {
				t.Errorf("install #%d: %s line present=%v, want %v\n%s", i+1, name, has, env[name] != "", body)
			}
		}
		for name, secret := range secretEnv {
			if strings.Contains(body, secret) {
				t.Errorf("install #%d: unit leaked %s", i+1, name)
			}
		}
		want := [][]string{
			{"systemctl", "--user", "daemon-reload"},
			{"systemctl", "--user", "enable", "ccmuxd"},
			{"systemctl", "--user", "restart", "ccmuxd"},
		}
		if !reflect.DeepEqual(r.calls, want) {
			t.Errorf("install #%d: systemctl calls = %v, want %v", i+1, r.calls, want)
		}
	}
}

func TestInstallerLinux_RestartFailureSurfacesOutput(t *testing.T) {
	r := &fakeRunner{failOn: "restart"}
	err := testInstaller(t.TempDir(), nil, r).installLinux()
	if err == nil || !strings.Contains(err.Error(), "simulated restart output") {
		t.Fatalf("installLinux error = %v, want systemctl restart output", err)
	}
}
