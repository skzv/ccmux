package setupwizard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/claudeconfig"
)

// --- pure entry / config helpers -----------------------------------

// TestCCMUXMCPEntry_ReadOnlyDefault — the headline contract: the entry
// points at the ccmux-mcp binary over stdio with no args by default.
func TestCCMUXMCPEntry_ReadOnlyDefault(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal(ccmuxMCPEntry(false), &got); err != nil {
		t.Fatal(err)
	}
	if got["command"] != "ccmux-mcp" {
		t.Errorf("command = %v, want ccmux-mcp", got["command"])
	}
	if got["type"] != "stdio" {
		t.Errorf("type = %v, want stdio — explicit type lets Claude pick the right transport", got["type"])
	}
	if args, _ := got["args"].([]any); len(args) != 0 {
		t.Errorf("args = %v, want empty (read-only is the safe default)", got["args"])
	}
}

// TestCCMUXMCPEntry_AllowMutateSetsFlag — pins the exact wire format so
// a refactor can't quietly switch to an env var or different flag.
func TestCCMUXMCPEntry_AllowMutateSetsFlag(t *testing.T) {
	var got claudeconfig.MCPServer
	if err := json.Unmarshal(ccmuxMCPEntry(true), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Args) != 1 || got.Args[0] != "--allow-mutate" {
		t.Errorf("Args = %v, want [--allow-mutate]", got.Args)
	}
}

// TestUserMCPConfig_SetEntryPreservesOtherServers — the user may
// already have other MCP servers (postgres, slack, …) with fields ccmux
// doesn't model (headers with auth tokens). Adding ccmux must not
// clobber or reshape them.
func TestUserMCPConfig_SetEntryPreservesOtherServers(t *testing.T) {
	home := withFakeClaudeHome(t)
	seedUserConfig(t, home, `{
  "mcpServers": {
    "postgres": {"type": "stdio", "command": "postgres-mcp", "args": ["--db", "mydb"]},
    "slack": {"type": "http", "url": "https://slack.local/mcp", "headers": {"Authorization": "Bearer xyz"}}
  }
}`)
	cfg, err := readUserMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.setEntry(ccmuxMCPName, ccmuxMCPEntry(false)); err != nil {
		t.Fatal(err)
	}
	if err := cfg.save(); err != nil {
		t.Fatal(err)
	}
	servers := readUserServers(t, home)
	if got := servers["postgres"]; got["command"] != "postgres-mcp" || len(got["args"].([]any)) != 2 {
		t.Errorf("postgres entry corrupted: %v", got)
	}
	if hdr, _ := servers["slack"]["headers"].(map[string]any); hdr["Authorization"] != "Bearer xyz" {
		t.Errorf("slack headers lost: %v", servers["slack"])
	}
	if servers["ccmux"]["command"] != "ccmux-mcp" {
		t.Errorf("ccmux entry not added: %v", servers)
	}
}

// TestUserMCPConfig_ReplaceExistingEntry — re-registering with another
// mode replaces the entry wholesale rather than merging args.
func TestUserMCPConfig_ReplaceExistingEntry(t *testing.T) {
	home := withFakeClaudeHome(t)
	seedUserConfig(t, home, `{"mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp", "args": ["--legacy-flag"]}}}`)
	cfg, err := readUserMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.setEntry(ccmuxMCPName, ccmuxMCPEntry(true)); err != nil {
		t.Fatal(err)
	}
	got, _ := cfg.entry(ccmuxMCPName)
	if len(got.Args) != 1 || got.Args[0] != "--allow-mutate" {
		t.Errorf("Args = %v, want [--allow-mutate] — re-register should fully replace, not merge args", got.Args)
	}
}

// TestUserMCPConfig_MissingFileIsEmpty — no ~/.claude.json yet reads as
// an empty config (nil maps initialized) rather than an error.
func TestUserMCPConfig_MissingFileIsEmpty(t *testing.T) {
	withFakeClaudeHome(t)
	cfg, err := readUserMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.exists {
		t.Error("exists = true for a missing file")
	}
	if err := cfg.setEntry(ccmuxMCPName, ccmuxMCPEntry(false)); err != nil {
		t.Fatalf("setEntry on an empty config: %v", err)
	}
}

// TestClaudeUserConfigPath_HonorsConfigDir — with CLAUDE_CONFIG_DIR set,
// Claude Code keeps .claude.json inside it.
func TestClaudeUserConfigPath_HonorsConfigDir(t *testing.T) {
	home := withFakeClaudeHome(t)
	if p, _ := claudeUserConfigPath(); p != filepath.Join(home, ".claude.json") {
		t.Errorf("default path = %s, want ~/.claude.json", p)
	}
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if p, _ := claudeUserConfigPath(); p != filepath.Join(dir, ".claude.json") {
		t.Errorf("path with CLAUDE_CONFIG_DIR = %s, want %s/.claude.json", p, dir)
	}
}

// TestContainsArg_ExactMatch — the idempotence detector. The wizard
// reads existing args to label "read-only" vs "--allow-mutate" and
// must not be fooled by partial matches.
func TestContainsArg_ExactMatch(t *testing.T) {
	cases := []struct {
		args []string
		want string
		hit  bool
	}{
		{[]string{"--allow-mutate"}, "--allow-mutate", true},
		{[]string{"--other", "--allow-mutate"}, "--allow-mutate", true},
		{[]string{}, "--allow-mutate", false},
		{nil, "--allow-mutate", false},
		{[]string{"--allow-mutate-other"}, "--allow-mutate", false}, // partial match must NOT trigger
	}
	for _, tc := range cases {
		if got := containsArg(tc.args, tc.want); got != tc.hit {
			t.Errorf("containsArg(%v, %q) = %v, want %v", tc.args, tc.want, got, tc.hit)
		}
	}
}

// --- where the registration lands ----------------------------------

// TestRegisterMCPForCLI_UsesClaudeCLIUserScope — regression: ccmux
// registered itself under mcpServers in ~/.claude/settings.json, a key
// Claude Code never reads. With the claude CLI available it must run
// exactly `claude mcp add-json --scope user ccmux <entry>`, which lands
// the entry in ~/.claude.json — where MCPStatus then finds it.
func TestRegisterMCPForCLI_UsesClaudeCLIUserScope(t *testing.T) {
	home := withFakeClaudeHome(t)
	calls := fakeClaudeCLI(t, home)

	var buf bytes.Buffer
	if err := RegisterMCPForCLI(context.Background(), &buf, false); err != nil {
		t.Fatalf("RegisterMCPForCLI: %v\n%s", err, buf.String())
	}
	want := []string{"mcp", "add-json", "--scope", "user", "ccmux", `{"type":"stdio","command":"ccmux-mcp","args":[]}`}
	if len(*calls) != 1 || strings.Join((*calls)[0], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("claude invocations = %q, want exactly %q", *calls, want)
	}
	if mode, ok, err := MCPStatus(); err != nil || !ok || mode != "read-only" {
		t.Errorf("MCPStatus = (%q, %v, %v), want registered read-only", mode, ok, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Error("registration touched ~/.claude/settings.json")
	}
}

// TestRegisterMCPForCLI_FallbackEditsClaudeJSON — without the claude CLI
// the entry is written straight into ~/.claude.json's top-level
// mcpServers, every other key survives verbatim (including numbers that
// don't fit a float64), a backup is kept, and MCPStatus agrees.
func TestRegisterMCPForCLI_FallbackEditsClaudeJSON(t *testing.T) {
	home := withFakeClaudeHome(t)
	noClaudeCLI(t)
	seedUserConfig(t, home, `{
  "numStartups": 42,
  "firstStartTime": 1698765432109876543,
  "projects": {"/Users/me/p": {"allowedTools": ["Bash"]}},
  "mcpServers": {"postgres": {"type": "stdio", "command": "postgres-mcp"}}
}`)

	var buf bytes.Buffer
	if err := RegisterMCPForCLI(context.Background(), &buf, true); err != nil {
		t.Fatalf("RegisterMCPForCLI: %v\n%s", err, buf.String())
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("~/.claude.json no longer valid JSON: %v\n%s", err, raw)
	}
	if string(top["firstStartTime"]) != "1698765432109876543" || string(top["numStartups"]) != "42" {
		t.Errorf("unrelated keys changed: firstStartTime=%s numStartups=%s", top["firstStartTime"], top["numStartups"])
	}
	if !strings.Contains(string(top["projects"]), "allowedTools") {
		t.Errorf("projects key lost: %s", top["projects"])
	}
	servers := readUserServers(t, home)
	if servers["postgres"]["command"] != "postgres-mcp" {
		t.Errorf("postgres server lost: %v", servers)
	}
	if args, _ := servers["ccmux"]["args"].([]any); servers["ccmux"]["command"] != "ccmux-mcp" || len(args) != 1 || args[0] != "--allow-mutate" {
		t.Errorf("ccmux entry = %v, want ccmux-mcp --allow-mutate", servers["ccmux"])
	}
	if mode, ok, _ := MCPStatus(); !ok || mode != "with --allow-mutate" {
		t.Errorf("MCPStatus = (%q, %v), want registered with --allow-mutate", mode, ok)
	}
	backups, _ := os.ReadDir(filepath.Join(home, ".claude", "backups"))
	if len(backups) == 0 {
		t.Error("~/.claude.json was modified without a backup")
	}
}

// TestRegisterMCPForCLI_RealExecFallsBackWhenCLIFails — drives the real
// exec seam with a `claude` stub that fails every command (the only
// `claude` on PATH, so no real CLI can run): registration must still
// land in ~/.claude.json via the direct edit.
func TestRegisterMCPForCLI_RealExecFallsBackWhenCLIFails(t *testing.T) {
	home := withFakeClaudeHome(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho 'unknown command' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	seedUserConfig(t, home, `{"numStartups": 7}`)

	var buf bytes.Buffer
	if err := RegisterMCPForCLI(context.Background(), &buf, false); err != nil {
		t.Fatalf("RegisterMCPForCLI: %v\n%s", err, buf.String())
	}
	if servers := readUserServers(t, home); servers["ccmux"]["command"] != "ccmux-mcp" {
		t.Fatalf("ccmux not registered in ~/.claude.json: %v\n%s", servers, buf.String())
	}
	if mode, ok, _ := MCPStatus(); !ok || mode != "read-only" {
		t.Errorf("MCPStatus = (%q, %v), want registered read-only", mode, ok)
	}
}

// TestRegisterMCPForCLI_ModeChangeRemovesThenAdds — switching modes
// goes through the CLI as remove + add-json (add-json won't overwrite).
func TestRegisterMCPForCLI_ModeChangeRemovesThenAdds(t *testing.T) {
	home := withFakeClaudeHome(t)
	calls := fakeClaudeCLI(t, home)
	seedUserConfig(t, home, `{"mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp", "args": []}}}`)

	var buf bytes.Buffer
	if err := RegisterMCPForCLI(context.Background(), &buf, true); err != nil {
		t.Fatalf("RegisterMCPForCLI: %v", err)
	}
	if len(*calls) != 2 || (*calls)[0][1] != "remove" || (*calls)[1][1] != "add-json" {
		t.Fatalf("claude invocations = %q, want remove then add-json", *calls)
	}
	if got := (*calls)[1][len((*calls)[1])-1]; !strings.Contains(got, `"--allow-mutate"`) {
		t.Errorf("add-json entry %s lacks --allow-mutate", got)
	}
	if mode, ok, _ := MCPStatus(); !ok || mode != "with --allow-mutate" {
		t.Errorf("MCPStatus = (%q, %v), want with --allow-mutate", mode, ok)
	}
	// The user opted into changing the file; the prior state is saved.
	if entries, _ := os.ReadDir(filepath.Join(home, ".claude", "backups")); len(entries) == 0 {
		t.Error("mode change must write a backup of ~/.claude.json; backups dir is empty")
	}
}

// TestRegisterMCPForCLI_SameModeIsNoOp — re-running with the same mode
// must not run the CLI or write anything (no backup piling up on every
// CI run that calls `ccmux mcp register` for idempotence).
func TestRegisterMCPForCLI_SameModeIsNoOp(t *testing.T) {
	home := withFakeClaudeHome(t)
	calls := fakeClaudeCLI(t, home)
	seedUserConfig(t, home, `{"mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp"}}}`)

	var buf bytes.Buffer
	if err := RegisterMCPForCLI(context.Background(), &buf, false); err != nil {
		t.Fatalf("RegisterMCPForCLI: %v", err)
	}
	if !strings.Contains(buf.String(), "already registered") {
		t.Errorf("expected 'already registered' on same-mode re-register; got:\n%s", buf.String())
	}
	if len(*calls) != 0 {
		t.Errorf("same-mode register ran claude: %q", *calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "backups")); !os.IsNotExist(err) {
		t.Error("backups dir was created for a no-op register; idempotence broken")
	}
}

// TestRegisterMCPForCLI_RemovesStaleSettingsEntry — the entry older
// ccmux left in ~/.claude/settings.json is removed (other settings and
// servers there survive), and the real registration is in
// ~/.claude.json.
func TestRegisterMCPForCLI_RemovesStaleSettingsEntry(t *testing.T) {
	home := withFakeClaudeHome(t)
	fakeClaudeCLI(t, home)
	seedSettings(t, filepath.Join(home, ".claude"), map[string]any{
		"model": "claude-opus-4-7",
		"mcpServers": map[string]any{
			"ccmux":    map[string]any{"type": "stdio", "command": "ccmux-mcp", "args": []any{"--allow-mutate"}},
			"postgres": map[string]any{"type": "stdio", "command": "postgres-mcp"},
		},
	})

	var buf bytes.Buffer
	if err := RegisterMCPForCLI(context.Background(), &buf, false); err != nil {
		t.Fatalf("RegisterMCPForCLI: %v", err)
	}
	s, err := claudeconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.MCPServers["ccmux"]; ok {
		t.Error("stale ccmux entry still in ~/.claude/settings.json")
	}
	if _, ok := s.MCPServers["postgres"]; !ok || s.Model != "claude-opus-4-7" {
		t.Errorf("unrelated settings lost: model=%q servers=%v", s.Model, s.MCPServers)
	}
	if _, ok, _ := MCPStatus(); !ok {
		t.Error("ccmux not registered in ~/.claude.json")
	}
}

// --- MCPStatus -------------------------------------------------------

// TestMCPStatus_ReadsClaudeJSONNotSettings — status must reflect what
// Claude Code actually loads: a ccmux entry only in settings.json (the
// old, ignored location) is NOT a registration; one in ~/.claude.json is.
func TestMCPStatus_ReadsClaudeJSONNotSettings(t *testing.T) {
	home := withFakeClaudeHome(t)
	seedSettings(t, filepath.Join(home, ".claude"), map[string]any{
		"mcpServers": map[string]any{"ccmux": map[string]any{"type": "stdio", "command": "ccmux-mcp"}},
	})
	if mode, ok, err := MCPStatus(); err != nil || ok {
		t.Errorf("settings.json-only entry reported as registered (%q, %v, %v)", mode, ok, err)
	}

	seedUserConfig(t, home, `{"mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp", "args": ["--allow-mutate"]}}}`)
	if mode, ok, err := MCPStatus(); err != nil || !ok || mode != "with --allow-mutate" {
		t.Errorf("MCPStatus = (%q, %v, %v), want registered with --allow-mutate", mode, ok, err)
	}
}

// TestMCPStatus_NotRegistered — clean config reports registered=false
// and an empty mode string.
func TestMCPStatus_NotRegistered(t *testing.T) {
	withFakeClaudeHome(t)
	mode, ok, err := MCPStatus()
	if err != nil {
		t.Fatalf("MCPStatus: %v", err)
	}
	if ok || mode != "" {
		t.Errorf("clean config: registered=%v mode=%q, want false/empty", ok, mode)
	}
}

// TestMCPStatus_RegisteredReadOnly — pinned mode label so the CLI
// status command's wording is stable.
func TestMCPStatus_RegisteredReadOnly(t *testing.T) {
	home := withFakeClaudeHome(t)
	seedUserConfig(t, home, `{"mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp"}}}`)
	mode, ok, err := MCPStatus()
	if err != nil {
		t.Fatalf("MCPStatus: %v", err)
	}
	if !ok {
		t.Fatal("registered=false but the entry is seeded")
	}
	if mode != "read-only" {
		t.Errorf("mode = %q, want read-only", mode)
	}
}

// TestMCPStatus_RegisteredAllowMutate — same, for the mutating mode.
func TestMCPStatus_RegisteredAllowMutate(t *testing.T) {
	home := withFakeClaudeHome(t)
	seedUserConfig(t, home, `{"mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp", "args": ["--allow-mutate"]}}}`)
	mode, ok, err := MCPStatus()
	if err != nil {
		t.Fatalf("MCPStatus: %v", err)
	}
	if !ok {
		t.Fatal("registered=false but the entry is seeded")
	}
	if mode != "with --allow-mutate" {
		t.Errorf("mode = %q, want 'with --allow-mutate'", mode)
	}
}

// --- the wizard step -------------------------------------------------

// TestStepMCP_BackupWrittenBeforeChange — Claude Code's user config is
// precious; the wizard MUST back it up before registering (CLI path
// included) so a botched merge has a recovery path.
func TestStepMCP_BackupWrittenBeforeChange(t *testing.T) {
	home := withFakeClaudeHome(t)
	fakeClaudeCLI(t, home)
	seedUserConfig(t, home, `{"numStartups": 3}`)

	var buf bytes.Buffer
	if err := stepMCP(withAssumeYes(context.Background()), &buf); err != nil {
		t.Fatalf("stepMCP: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "backups"))
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("backup dir is empty — ~/.claude.json was mutated without a backup")
	}
}

// TestStepMCP_SkipsWhenClaudeNotInstalled — no `claude` on PATH and no
// ~/.claude.json: the wizard prints a "skip" line and bails cleanly. No
// write, no error, so `ccmux setup` doesn't fail for non-Claude users.
func TestStepMCP_SkipsWhenClaudeNotInstalled(t *testing.T) {
	home := withFakeClaudeHome(t)
	noClaudeCLI(t)

	var buf bytes.Buffer
	if err := stepMCP(context.Background(), &buf); err != nil {
		t.Fatalf("stepMCP errored when it should have skipped: %v", err)
	}
	if !strings.Contains(buf.String(), "Claude Code not on PATH") {
		t.Errorf("expected skip message; got:\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Error("skip path wrote ~/.claude.json")
	}
}

// TestStepMCP_IdempotentSkipsWhenAlreadyRegistered — a second wizard
// run sees the existing entry and reports its mode without prompting
// or writing.
func TestStepMCP_IdempotentSkipsWhenAlreadyRegistered(t *testing.T) {
	home := withFakeClaudeHome(t)
	calls := fakeClaudeCLI(t, home)
	seedUserConfig(t, home, `{"mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp"}}}`)

	var buf bytes.Buffer
	if err := stepMCP(withAssumeYes(context.Background()), &buf); err != nil {
		t.Fatalf("stepMCP: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "already wired") {
		t.Errorf("expected idempotent skip message; got:\n%s", out)
	}
	if !strings.Contains(out, "read-only") {
		t.Errorf("expected mode label 'read-only' on existing read-only entry; got:\n%s", out)
	}
	if len(*calls) != 0 {
		t.Errorf("already-registered run invoked claude: %q", *calls)
	}
}

// TestStepMCP_IdempotentReportsAllowMutateMode — second-run path when
// the prior registration enabled --allow-mutate.
func TestStepMCP_IdempotentReportsAllowMutateMode(t *testing.T) {
	home := withFakeClaudeHome(t)
	fakeClaudeCLI(t, home)
	seedUserConfig(t, home, `{"mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp", "args": ["--allow-mutate"]}}}`)

	var buf bytes.Buffer
	if err := stepMCP(withAssumeYes(context.Background()), &buf); err != nil {
		t.Fatalf("stepMCP: %v", err)
	}
	if !strings.Contains(buf.String(), "--allow-mutate") {
		t.Errorf("expected mode label '--allow-mutate' on existing mutating entry; got:\n%s", buf.String())
	}
}

// TestStepMCP_FreshInstallRegisters — the green-field path in --yes
// mode: the wizard takes the affirmative for "register?" and the safe
// negative for "--allow-mutate?", registering read-only in the user
// scope Claude Code reads.
func TestStepMCP_FreshInstallRegisters(t *testing.T) {
	home := withFakeClaudeHome(t)
	calls := fakeClaudeCLI(t, home)

	var buf bytes.Buffer
	if err := stepMCP(withAssumeYes(context.Background()), &buf); err != nil {
		t.Fatalf("stepMCP: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0][1] != "add-json" || (*calls)[0][3] != "user" {
		t.Fatalf("claude invocations = %q, want one add-json --scope user", *calls)
	}
	mode, ok, err := MCPStatus()
	if err != nil || !ok {
		t.Fatalf("ccmux entry not registered: ok=%v err=%v", ok, err)
	}
	if mode != "read-only" {
		t.Errorf("mode = %q, want read-only (--yes mode picks the safe default)", mode)
	}
	if !strings.Contains(buf.String(), "wired ccmux-mcp into Claude Code") {
		t.Errorf("expected success message; got:\n%s", buf.String())
	}
}

// TestStepMCP_FreshInstallPreservesExistingSettings — without the CLI
// the wizard edits ~/.claude.json directly; unrelated keys and other
// MCP servers survive the round-trip, and a backup is written first.
func TestStepMCP_FreshInstallPreservesExistingSettings(t *testing.T) {
	home := withFakeClaudeHome(t)
	noClaudeCLI(t)
	seedUserConfig(t, home, `{
  "theme": "dark",
  "customKey": "customValue",
  "mcpServers": {"postgres": {"type": "stdio", "command": "postgres-mcp"}}
}`)

	var buf bytes.Buffer
	if err := stepMCP(withAssumeYes(context.Background()), &buf); err != nil {
		t.Fatalf("stepMCP: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode ~/.claude.json: %v", err)
	}
	if got["theme"] != "dark" || got["customKey"] != "customValue" {
		t.Errorf("unrelated keys lost: %v", got)
	}
	servers, _ := got["mcpServers"].(map[string]any)
	if _, ok := servers["postgres"]; !ok {
		t.Error("postgres MCP server lost")
	}
	if _, ok := servers["ccmux"]; !ok {
		t.Error("ccmux MCP server not added")
	}
	backups, err := os.ReadDir(filepath.Join(home, ".claude", "backups"))
	if err != nil || len(backups) == 0 {
		t.Errorf("no backup of ~/.claude.json before the change (err=%v)", err)
	}
}

// --- helpers -----------------------------------------------------

// withFakeClaudeHome points HOME at a temp dir and clears
// CLAUDE_CONFIG_DIR, so ~/.claude.json and ~/.claude/ both live under
// it and no test can touch the real Claude Code config. Returns HOME.
func withFakeClaudeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

// fakeClaudeCLI makes the claude CLI "available" and replaces it with an
// in-process fake that records every invocation and applies
// `mcp add-json --scope user` / `mcp remove --scope user` to
// ~/.claude.json the way Claude Code does. Returns the recorded argv.
func fakeClaudeCLI(t *testing.T, home string) *[][]string {
	t.Helper()
	calls := &[][]string{}
	origLook, origRun := lookClaudeCLI, runClaudeMCP
	t.Cleanup(func() { lookClaudeCLI, runClaudeMCP = origLook, origRun })
	lookClaudeCLI = func() bool { return true }
	runClaudeMCP = func(_ context.Context, args ...string) ([]byte, error) {
		*calls = append(*calls, append([]string(nil), args...))
		if len(args) < 5 || args[0] != "mcp" || args[2] != "--scope" || args[3] != "user" {
			return nil, errors.New("fake claude: unexpected invocation")
		}
		path := filepath.Join(home, ".claude.json")
		top := map[string]json.RawMessage{}
		if b, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(b, &top); err != nil {
				return nil, err
			}
		}
		servers := map[string]json.RawMessage{}
		if raw, ok := top["mcpServers"]; ok {
			if err := json.Unmarshal(raw, &servers); err != nil {
				return nil, err
			}
		}
		switch args[1] {
		case "add-json":
			if _, exists := servers[args[4]]; exists {
				return []byte("MCP server already exists"), errors.New("exit status 1")
			}
			servers[args[4]] = json.RawMessage(args[5])
		case "remove":
			delete(servers, args[4])
		default:
			return nil, errors.New("fake claude: unknown mcp subcommand")
		}
		b, _ := json.Marshal(servers)
		top["mcpServers"] = b
		out, _ := json.Marshal(top)
		return nil, os.WriteFile(path, out, 0o600)
	}
	return calls
}

// noClaudeCLI makes the claude CLI unavailable; running it fails the
// test.
func noClaudeCLI(t *testing.T) {
	t.Helper()
	origLook, origRun := lookClaudeCLI, runClaudeMCP
	t.Cleanup(func() { lookClaudeCLI, runClaudeMCP = origLook, origRun })
	lookClaudeCLI = func() bool { return false }
	runClaudeMCP = func(context.Context, ...string) ([]byte, error) {
		t.Error("claude CLI invoked although it is unavailable")
		return nil, errors.New("unavailable")
	}
}

// seedUserConfig writes body as ~/.claude.json.
func seedUserConfig(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readUserServers decodes ~/.claude.json's top-level mcpServers.
func readUserServers(t *testing.T, home string) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("decode ~/.claude.json: %v\n%s", err, raw)
	}
	return top.MCPServers
}

// seedSettings writes the given map as settings.json in dir (the
// ~/.claude directory) so a test can exercise the stale-entry paths.
func seedSettings(t *testing.T, dir string, contents map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(contents, "", "  ")
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), b, 0o644); err != nil {
		t.Fatalf("write seed settings: %v", err)
	}
}
