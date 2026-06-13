package setupwizard

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/claudeconfig"
)

// TestRegisterCCMUXMCP_AddsEntry — the headline pure-function
// contract. An empty Settings.MCPServers map gets the `ccmux` entry,
// pointed at the binary, with no args by default.
func TestRegisterCCMUXMCP_AddsEntry(t *testing.T) {
	s := &claudeconfig.Settings{}
	registerCCMUXMCP(s, false)

	got, ok := s.MCPServers["ccmux"]
	if !ok {
		t.Fatal("registerCCMUXMCP did not add the ccmux entry")
	}
	if got.Command != "ccmux-mcp" {
		t.Errorf("Command = %q, want ccmux-mcp", got.Command)
	}
	if got.Type != "stdio" {
		t.Errorf("Type = %q, want stdio — explicit type lets older Claude clients pick the right transport", got.Type)
	}
	if len(got.Args) != 0 {
		t.Errorf("Args = %v, want empty (read-only is the safe default)", got.Args)
	}
}

// TestRegisterCCMUXMCP_AllowMutateSetsFlag — when the user opts in
// the args slice carries --allow-mutate. Pins the exact wire format
// so a refactor can't quietly switch to an env var or different flag.
func TestRegisterCCMUXMCP_AllowMutateSetsFlag(t *testing.T) {
	s := &claudeconfig.Settings{}
	registerCCMUXMCP(s, true)

	got := s.MCPServers["ccmux"]
	if len(got.Args) != 1 || got.Args[0] != "--allow-mutate" {
		t.Errorf("Args = %v, want [--allow-mutate]", got.Args)
	}
}

// TestRegisterCCMUXMCP_PreservesExistingServers — the user might
// already have other MCP servers configured (postgres, slack,
// whatever). Adding ccmux MUST NOT clobber them.
func TestRegisterCCMUXMCP_PreservesExistingServers(t *testing.T) {
	s := &claudeconfig.Settings{
		MCPServers: map[string]claudeconfig.MCPServer{
			"postgres": {Type: "stdio", Command: "postgres-mcp", Args: []string{"--db", "mydb"}},
			"slack":    {Type: "http", URL: "https://slack.local/mcp"},
		},
	}
	registerCCMUXMCP(s, false)

	if _, ok := s.MCPServers["postgres"]; !ok {
		t.Error("registerCCMUXMCP clobbered existing 'postgres' MCP entry")
	}
	if got := s.MCPServers["postgres"]; got.Command != "postgres-mcp" || len(got.Args) != 2 {
		t.Errorf("postgres entry corrupted: %+v", got)
	}
	if _, ok := s.MCPServers["slack"]; !ok {
		t.Error("registerCCMUXMCP clobbered existing 'slack' MCP entry")
	}
	if _, ok := s.MCPServers["ccmux"]; !ok {
		t.Error("ccmux entry was not added alongside the existing ones")
	}
}

// TestRegisterCCMUXMCP_ReplaceExistingCCMUXEntry — re-running the
// step with a different mode (e.g. the user previously registered
// read-only and now wants --allow-mutate) must replace the entry,
// not merge. The wizard's idempotent check guards against re-prompts;
// this confirms the underlying function does the right thing if
// called.
func TestRegisterCCMUXMCP_ReplaceExistingCCMUXEntry(t *testing.T) {
	s := &claudeconfig.Settings{
		MCPServers: map[string]claudeconfig.MCPServer{
			"ccmux": {Type: "stdio", Command: "ccmux-mcp", Args: []string{"--legacy-flag"}},
		},
	}
	registerCCMUXMCP(s, true)

	got := s.MCPServers["ccmux"]
	if len(got.Args) != 1 || got.Args[0] != "--allow-mutate" {
		t.Errorf("Args = %v, want [--allow-mutate] — re-register should fully replace, not merge args", got.Args)
	}
}

// TestRegisterCCMUXMCP_NilMapInitialized — the function must handle
// the zero-valued Settings struct without panicking on nil map writes.
// Easy to miss in code review; pin it.
func TestRegisterCCMUXMCP_NilMapInitialized(t *testing.T) {
	s := &claudeconfig.Settings{}
	if s.MCPServers != nil {
		t.Fatal("test setup expects nil MCPServers map")
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registerCCMUXMCP panicked on nil map: %v", r)
		}
	}()
	registerCCMUXMCP(s, false)
	if s.MCPServers == nil {
		t.Error("MCPServers map still nil after register")
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

// TestStepMCP_SkipsWhenClaudeNotInstalled — when `claude` isn't on
// PATH the wizard prints a "skip" line and bails cleanly. No write,
// no error. Important so the step doesn't make `ccmux setup` fail
// noisily for users who don't use Claude Code.
func TestStepMCP_SkipsWhenClaudeNotInstalled(t *testing.T) {
	withFakeClaudeDir(t)
	// Force LookPath to fail by setting PATH to a dir with no `claude`.
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	var buf bytes.Buffer
	if err := stepMCP(context.Background(), &buf); err != nil {
		t.Fatalf("stepMCP errored when it should have skipped: %v", err)
	}
	if !strings.Contains(buf.String(), "Claude Code not on PATH") {
		t.Errorf("expected skip message; got:\n%s", buf.String())
	}
}

// TestStepMCP_IdempotentSkipsWhenAlreadyRegistered — second wizard
// run sees the existing entry and reports its mode without prompting
// or writing. Pin so a refactor of the wizard chrome doesn't break
// idempotence (the property that lets users re-run setup freely).
func TestStepMCP_IdempotentSkipsWhenAlreadyRegistered(t *testing.T) {
	dir := withFakeClaudeDir(t)
	stubClaudeOnPath(t)

	// Seed the settings file with a ccmux entry already there.
	seedSettings(t, dir, map[string]any{
		"mcpServers": map[string]any{
			"ccmux": map[string]any{"type": "stdio", "command": "ccmux-mcp"},
		},
	})

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
}

// TestStepMCP_IdempotentReportsAllowMutateMode — second-run path
// when the prior registration enabled --allow-mutate. The message
// must reflect that so the user can tell the mode without opening
// the file.
func TestStepMCP_IdempotentReportsAllowMutateMode(t *testing.T) {
	dir := withFakeClaudeDir(t)
	stubClaudeOnPath(t)

	seedSettings(t, dir, map[string]any{
		"mcpServers": map[string]any{
			"ccmux": map[string]any{
				"type":    "stdio",
				"command": "ccmux-mcp",
				"args":    []any{"--allow-mutate"},
			},
		},
	})

	var buf bytes.Buffer
	if err := stepMCP(withAssumeYes(context.Background()), &buf); err != nil {
		t.Fatalf("stepMCP: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "--allow-mutate") {
		t.Errorf("expected mode label '--allow-mutate' on existing mutating entry; got:\n%s", out)
	}
}

// TestStepMCP_FreshInstallRegisters — the green-field path with
// --yes mode: settings doesn't have a ccmux entry, the wizard
// accepts the default (register), and the file ends with the
// ccmux entry present.
//
// --yes mode takes the AFFIRMATIVE for the "register?" prompt and
// the NEGATIVE for the "--allow-mutate?" prompt (the safe defaults).
// So we expect read-only registration.
func TestStepMCP_FreshInstallRegisters(t *testing.T) {
	dir := withFakeClaudeDir(t)
	stubClaudeOnPath(t)

	var buf bytes.Buffer
	if err := stepMCP(withAssumeYes(context.Background()), &buf); err != nil {
		t.Fatalf("stepMCP: %v", err)
	}

	// Settings file should now exist with the ccmux entry.
	s, err := claudeconfig.ReadSettings()
	if err != nil {
		t.Fatalf("re-read settings: %v", err)
	}
	entry, ok := s.MCPServers["ccmux"]
	if !ok {
		t.Fatal("ccmux entry not written")
	}
	if entry.Command != "ccmux-mcp" {
		t.Errorf("Command = %q", entry.Command)
	}
	if len(entry.Args) != 0 {
		t.Errorf("Args = %v, want empty (--yes mode picks safe default = read-only)", entry.Args)
	}
	// The success line should appear.
	if !strings.Contains(buf.String(), "wired ccmux-mcp into Claude Code") {
		t.Errorf("expected success message; got:\n%s", buf.String())
	}
	_ = dir
}

// TestStepMCP_FreshInstallPreservesExistingSettings — when the
// settings file already has unrelated keys (model, theme, other
// MCP servers), they survive the round-trip.
func TestStepMCP_FreshInstallPreservesExistingSettings(t *testing.T) {
	dir := withFakeClaudeDir(t)
	stubClaudeOnPath(t)

	seedSettings(t, dir, map[string]any{
		"model":       "claude-opus-4-7",
		"effortLevel": "high",
		"mcpServers": map[string]any{
			"postgres": map[string]any{"type": "stdio", "command": "postgres-mcp"},
		},
		"customKey": "customValue", // unknown to claudeconfig — must survive in Extra
	})

	var buf bytes.Buffer
	if err := stepMCP(withAssumeYes(context.Background()), &buf); err != nil {
		t.Fatalf("stepMCP: %v", err)
	}

	// Read back the JSON directly so we can verify the unknown
	// custom key survived. claudeconfig stashes those in Extra but
	// the disk file is what matters for the user's editor.
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("read raw settings: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode raw settings: %v", err)
	}
	if got["model"] != "claude-opus-4-7" {
		t.Errorf("model lost: %v", got["model"])
	}
	if got["customKey"] != "customValue" {
		t.Errorf("custom key lost: %v", got["customKey"])
	}
	mcpServers, _ := got["mcpServers"].(map[string]any)
	if _, ok := mcpServers["postgres"]; !ok {
		t.Error("postgres MCP server lost")
	}
	if _, ok := mcpServers["ccmux"]; !ok {
		t.Error("ccmux MCP server not added")
	}
}

// TestRegisterMCPForCLI_SameModeIsNoOp — the CLI register path.
// Re-running with the same mode (read-only when already read-only,
// mutate when already mutate) must NOT write a new backup. Otherwise
// the backups dir fills up on every CI run that calls `ccmux mcp
// register` for idempotence.
func TestRegisterMCPForCLI_SameModeIsNoOp(t *testing.T) {
	dir := withFakeClaudeDir(t)
	stubClaudeOnPath(t)
	seedSettings(t, dir, map[string]any{
		"mcpServers": map[string]any{
			"ccmux": map[string]any{"type": "stdio", "command": "ccmux-mcp"},
		},
	})

	var buf bytes.Buffer
	if err := RegisterMCPForCLI(context.Background(), &buf, false); err != nil {
		t.Fatalf("RegisterMCPForCLI: %v", err)
	}
	if !strings.Contains(buf.String(), "already registered") {
		t.Errorf("expected 'already registered' on same-mode re-register; got:\n%s", buf.String())
	}
	// Backups dir should NOT exist (we never wrote).
	if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
		t.Error("backups dir was created for a no-op register; idempotence broken")
	}
}

// TestRegisterMCPForCLI_ModeChangeRewrites — switching modes
// (read-only → mutate or vice-versa) writes the new entry AND a
// backup. The user opted into changing the file; we save the prior
// state.
func TestRegisterMCPForCLI_ModeChangeRewrites(t *testing.T) {
	dir := withFakeClaudeDir(t)
	stubClaudeOnPath(t)
	seedSettings(t, dir, map[string]any{
		"mcpServers": map[string]any{
			"ccmux": map[string]any{"type": "stdio", "command": "ccmux-mcp"},
		},
	})

	var buf bytes.Buffer
	if err := RegisterMCPForCLI(context.Background(), &buf, true); err != nil {
		t.Fatalf("RegisterMCPForCLI: %v", err)
	}

	s, _ := claudeconfig.ReadSettings()
	got := s.MCPServers["ccmux"]
	if !containsArg(got.Args, "--allow-mutate") {
		t.Error("mode change to mutate didn't add --allow-mutate to args")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	if len(entries) == 0 {
		t.Error("mode change must write a backup; backups dir is empty")
	}
}

// TestMCPStatus_NotRegistered — clean settings should report
// registered=false and an empty mode string.
func TestMCPStatus_NotRegistered(t *testing.T) {
	withFakeClaudeDir(t)
	mode, ok, err := MCPStatus()
	if err != nil {
		t.Fatalf("MCPStatus: %v", err)
	}
	if ok {
		t.Errorf("registered=true on clean settings, got mode=%q", mode)
	}
	if mode != "" {
		t.Errorf("mode = %q, want empty when not registered", mode)
	}
}

// TestMCPStatus_RegisteredReadOnly — pinned mode label so the CLI
// status command's wording is stable.
func TestMCPStatus_RegisteredReadOnly(t *testing.T) {
	dir := withFakeClaudeDir(t)
	seedSettings(t, dir, map[string]any{
		"mcpServers": map[string]any{
			"ccmux": map[string]any{"type": "stdio", "command": "ccmux-mcp"},
		},
	})
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
	dir := withFakeClaudeDir(t)
	seedSettings(t, dir, map[string]any{
		"mcpServers": map[string]any{
			"ccmux": map[string]any{
				"type":    "stdio",
				"command": "ccmux-mcp",
				"args":    []any{"--allow-mutate"},
			},
		},
	})
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

// TestStepMCP_BackupWrittenBeforeChange — Claude Code's settings is
// user-edited; the wizard MUST back up before mutating so a botched
// merge has a recovery path. The backup dir gets a timestamped file.
func TestStepMCP_BackupWrittenBeforeChange(t *testing.T) {
	dir := withFakeClaudeDir(t)
	stubClaudeOnPath(t)

	seedSettings(t, dir, map[string]any{"model": "claude-opus-4-7"})

	var buf bytes.Buffer
	if err := stepMCP(withAssumeYes(context.Background()), &buf); err != nil {
		t.Fatalf("stepMCP: %v", err)
	}

	backupDir := filepath.Join(dir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("backup dir is empty — settings was mutated without a backup")
	}
}

// --- helpers -----------------------------------------------------

// stubClaudeOnPath drops a tiny `claude` executable into a temp dir
// and prepends it to PATH so stepMCP's `exec.LookPath("claude")`
// returns success. Avoids assuming the test runner has Claude Code
// installed.
func stubClaudeOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	stub := filepath.Join(bin, "claude")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho stub\n"), 0o755); err != nil {
		t.Fatalf("write claude stub: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Confirm LookPath finds it — fail fast if the sandboxing didn't
	// take so the actual tests aren't a confusing red herring.
	if _, err := exec.LookPath("claude"); err != nil {
		t.Fatalf("claude stub didn't make it onto PATH: %v", err)
	}
}

// seedSettings writes the given map as the settings.json in the fake
// Claude dir so the test can exercise an "already-has-X" path.
func seedSettings(t *testing.T, dir string, contents map[string]any) {
	t.Helper()
	p := filepath.Join(dir, "settings.json")
	b, err := json.MarshalIndent(contents, "", "  ")
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("write seed settings: %v", err)
	}
}

// withFakeClaudeDir points claudeconfig at a tempdir for the duration
// of the test. Mirrors the helper in internal/claudeconfig but kept
// here to avoid a test-only export. Returns the directory so callers
// can read written artifacts directly.
func withFakeClaudeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	return dir
}
