package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAll_CanonicalOrder pins the canonical agent ordering. Order is
// load-bearing: pickers default to the first installed entry, so a
// reshuffle that put Codex first would silently change every new
// user's default agent.
func TestAll_CanonicalOrder(t *testing.T) {
	got := All()
	if len(got) != 4 {
		t.Fatalf("All() len = %d, want 4", len(got))
	}
	wantIDs := []ID{IDClaude, IDCodex, IDAntigravity, IDCursor}
	for i, a := range got {
		if a.ID() != wantIDs[i] {
			t.Errorf("All()[%d].ID() = %q, want %q", i, a.ID(), wantIDs[i])
		}
	}
}

// TestParseID covers every shape the sidecar / config / CLI flag might
// hand us. The "" → false case matters because the daemon defaults
// missing values to claude only when ParseID reports !ok.
func TestParseID(t *testing.T) {
	cases := []struct {
		in     string
		want   ID
		wantOK bool
	}{
		{"claude", IDClaude, true},
		{"CLAUDE", IDClaude, true},
		{"  Claude  ", IDClaude, true},
		{"codex", IDCodex, true},
		{"antigravity", IDAntigravity, true},
		{"gemini", IDAntigravity, true}, // back-compat alias
		{"cursor", IDCursor, true},
		{"", "", false},
		{"   ", "", false},
		{"gpt", "", false},
		{"claude-3", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := ParseID(tc.in)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("ParseID(%q) = (%q, %v), want (%q, %v)",
					tc.in, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestByID_KnownAndEmptyFallback — known IDs return the matching
// concrete; the empty string falls back to claude (the back-compat
// shim for projects scaffolded before the sidecar existed).
func TestByID_KnownAndEmptyFallback(t *testing.T) {
	cases := []struct {
		in   ID
		want ID
	}{
		{IDClaude, IDClaude},
		{IDCodex, IDCodex},
		{IDAntigravity, IDAntigravity},
		{"gemini", IDAntigravity}, // back-compat alias for projects scaffolded before the rebrand
		{IDCursor, IDCursor},
		{"", IDClaude}, // back-compat
	}
	for _, tc := range cases {
		if got := ByID(tc.in).ID(); got != tc.want {
			t.Errorf("ByID(%q).ID() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestByID_PanicsOnUnknown — ByID is the unchecked path; callers that
// take user input must go through ParseID. This test pins that
// distinction so a future "let's just be permissive in ByID" refactor
// trips here.
func TestByID_PanicsOnUnknown(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unknown ID")
		}
	}()
	_ = ByID("imaginary-llm-9000")
}

// TestDefault — locked at claude. If this ever changes, the migration
// guide for every existing ccmux user has to mention it.
func TestDefault(t *testing.T) {
	if got := Default().ID(); got != IDClaude {
		t.Errorf("Default().ID() = %q, want claude — changing this is a user-visible default flip", got)
	}
}

// TestAgent_Identity_Stable covers each Agent's identity quartet
// (ID, DisplayName, Binary, …) so a typo in any one of them fails the
// test instead of shipping silently to users.
func TestAgent_Identity_Stable(t *testing.T) {
	cases := []struct {
		a        Agent
		id       ID
		binary   string
		display  string
		cfgBase  string // last path segment of ConfigRoot
		transRel string // path relative to ConfigRoot for transcripts
	}{
		{Claude{}, IDClaude, "claude", "Claude Code", ".claude", "projects"},
		{Codex{}, IDCodex, "codex", "Codex", ".codex", "sessions"},
		{Antigravity{}, IDAntigravity, "agy", "Antigravity CLI", ".gemini/antigravity-cli", "conversations"},
		{Cursor{}, IDCursor, "cursor-agent", "Cursor", ".cursor", "sessions"},
	}
	for _, tc := range cases {
		t.Run(string(tc.id), func(t *testing.T) {
			if tc.a.ID() != tc.id {
				t.Errorf("ID() = %q, want %q", tc.a.ID(), tc.id)
			}
			if tc.a.Binary() != tc.binary {
				t.Errorf("Binary() = %q, want %q", tc.a.Binary(), tc.binary)
			}
			if tc.a.DisplayName() != tc.display {
				t.Errorf("DisplayName() = %q, want %q", tc.a.DisplayName(), tc.display)
			}
			home := "/home/test"
			cfg := tc.a.ConfigRoot(home)
			if !strings.HasSuffix(cfg, "/"+tc.cfgBase) {
				t.Errorf("ConfigRoot(%q) = %q, want suffix /%s", home, cfg, tc.cfgBase)
			}
			trans := tc.a.TranscriptsRoot(home)
			if !strings.HasSuffix(trans, "/"+tc.cfgBase+"/"+tc.transRel) {
				t.Errorf("TranscriptsRoot(%q) = %q, want suffix /%s/%s",
					home, trans, tc.cfgBase, tc.transRel)
			}
		})
	}
}

// TestAgent_LaunchCmd_NewVsContinue — `continueFlag=true` must wire in
// each agent's latest-resume dialect so resuming a project actually
// resumes the agent's prior conversation. Off-by-one here would mean
// every "attach existing" lands the user in a brand-new chat.
func TestAgent_LaunchCmd_NewVsContinue(t *testing.T) {
	for _, a := range All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			fresh := a.LaunchCmd(false)
			cont := a.LaunchCmd(true)
			if !strings.HasPrefix(fresh, a.Binary()) {
				t.Errorf("fresh LaunchCmd = %q, expected to start with binary %q",
					fresh, a.Binary())
			}
			switch a.ID() {
			case IDCursor:
				if !strings.Contains(cont, " resume") {
					t.Errorf("continue LaunchCmd = %q, expected resume subcommand", cont)
				}
			default:
				if !strings.Contains(cont, "--continue") {
					t.Errorf("continue LaunchCmd = %q, expected --continue", cont)
				}
			}
			// The fallback chain must end in `|| sh` so the pane stays
			// alive on minimal hosts without zsh (typical Linux CI). zsh
			// is still nice when present, but sh is the POSIX guarantee.
			if !strings.Contains(cont, "|| sh") {
				t.Errorf("continue LaunchCmd = %q, missing `|| sh` POSIX fallback", cont)
			}
		})
	}
}

// TestAgent_InitialPrompt_SubstitutesNameAndDesc — every agent's
// prompt template must echo the user's name + description back; a
// regression here would mean Claude/Codex/Antigravity all start their
// first session asking generic questions instead of the user's
// project-specific one.
func TestAgent_InitialPrompt_SubstitutesNameAndDesc(t *testing.T) {
	for _, a := range All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			got := a.InitialPrompt("auth-redesign", "rebuild login with passkeys")
			if !strings.Contains(got, "auth-redesign") {
				t.Errorf("%s prompt missing project name: %q", a.ID(), got)
			}
			if !strings.Contains(got, "rebuild login with passkeys") {
				t.Errorf("%s prompt missing description: %q", a.ID(), got)
			}
		})
	}
}

// TestAgent_InitialPrompt_EmptyDescGetsFallback — when the user
// scaffolds without -d, every agent's prompt must still produce
// something useful instead of dangling sentence fragments.
func TestAgent_InitialPrompt_EmptyDescGetsFallback(t *testing.T) {
	for _, a := range All() {
		t.Run(string(a.ID()), func(t *testing.T) {
			got := a.InitialPrompt("p", "")
			if !strings.Contains(got, "no description yet") {
				t.Errorf("%s prompt missing empty-desc fallback hint: %q", a.ID(), got)
			}
		})
	}
}

// TestClaude_Classify_DelegatesToInternalClaude — Claude's classifier
// is just a thin wrapper around internal/claude. Same pane input must
// produce the same State string on both sides; a divergence would mean
// the daemon's behavior changes depending on which call site classifies.
func TestClaude_Classify_DelegatesToInternalClaude(t *testing.T) {
	// Empty pane: both implementations should report unknown.
	if got := (Claude{}).Classify("", time.Now(), 3*time.Second); got != StateUnknown {
		t.Errorf("empty pane: Claude.Classify = %q, want unknown", got)
	}
	// Pane content with Claude's box-drawing prompt should be
	// recognized as needs_input when the pane has been idle long
	// enough. Use the actual characters internal/claude looks for so
	// the test is unambiguous.
	pane := "some Claude output here\n\n╭─────────╮\n│ > write a function │\n╰─────────╯"
	stale := time.Now().Add(-10 * time.Second)
	if got := (Claude{}).Classify(pane, stale, 3*time.Second); got != StateNeedsInput {
		t.Errorf("idle Claude prompt: got %q, want needs_input", got)
	}
}

// TestCodex_Classify_IdleHeuristic — until we have testdata-pinned
// Codex pane samples, the classifier is a quiet-pane heuristic. Pin
// the three branches (unknown / active / needs_input) so a future
// tightening doesn't silently flip the contract.
func TestCodex_Classify_IdleHeuristic(t *testing.T) {
	if got := (Codex{}).Classify("", time.Now(), 3*time.Second); got != StateUnknown {
		t.Errorf("empty pane: Codex.Classify = %q, want unknown", got)
	}
	if got := (Codex{}).Classify("recent output", time.Now(), 3*time.Second); got != StateActive {
		t.Errorf("fresh output: Codex.Classify = %q, want active", got)
	}
	stale := time.Now().Add(-10 * time.Second)
	if got := (Codex{}).Classify("old output", stale, 3*time.Second); got != StateNeedsInput {
		t.Errorf("stale output: Codex.Classify = %q, want needs_input", got)
	}
}

// TestAntigravity_Classify_IdleHeuristic mirrors the Codex test. Same
// stub semantics today.
func TestAntigravity_Classify_IdleHeuristic(t *testing.T) {
	if got := (Antigravity{}).Classify("", time.Now(), 3*time.Second); got != StateUnknown {
		t.Errorf("empty pane: Antigravity.Classify = %q, want unknown", got)
	}
	if got := (Antigravity{}).Classify("recent output", time.Now(), 3*time.Second); got != StateActive {
		t.Errorf("fresh output: Antigravity.Classify = %q, want active", got)
	}
	stale := time.Now().Add(-10 * time.Second)
	if got := (Antigravity{}).Classify("old output", stale, 3*time.Second); got != StateNeedsInput {
		t.Errorf("stale output: Antigravity.Classify = %q, want needs_input", got)
	}
}

func TestCursor_Classify_IdleHeuristic(t *testing.T) {
	if got := (Cursor{}).Classify("", time.Now(), 3*time.Second); got != StateUnknown {
		t.Errorf("empty pane: Cursor.Classify = %q, want unknown", got)
	}
	if got := (Cursor{}).Classify("recent output", time.Now(), 3*time.Second); got != StateActive {
		t.Errorf("fresh output: Cursor.Classify = %q, want active", got)
	}
	stale := time.Now().Add(-10 * time.Second)
	if got := (Cursor{}).Classify("old output", stale, 3*time.Second); got != StateNeedsInput {
		t.Errorf("stale output: Cursor.Classify = %q, want needs_input", got)
	}
}

// TestAllInstalled_RespectsHook injects a fake binary detector so the
// test doesn't depend on whatever's actually on the dev machine's
// PATH. Verifies AllInstalled returns the right subset and preserves
// canonical order.
func TestAllInstalled_RespectsHook(t *testing.T) {
	orig := installLookupHook
	defer func() { installLookupHook = orig }()

	// Scenario A: only claude installed.
	installLookupHook = func(_ context.Context, bin string) bool {
		return bin == "claude"
	}
	got := AllInstalled(context.Background())
	if len(got) != 1 || got[0].ID() != IDClaude {
		t.Fatalf("only-claude scenario: got %v", agentIDs(got))
	}

	// Scenario B: claude + antigravity, no codex.
	installLookupHook = func(_ context.Context, bin string) bool {
		return bin == "claude" || bin == "agy"
	}
	got = AllInstalled(context.Background())
	if len(got) != 2 || got[0].ID() != IDClaude || got[1].ID() != IDAntigravity {
		t.Errorf("claude+antigravity scenario: got %v (order/contents wrong)", agentIDs(got))
	}

	// Scenario C: nothing installed → empty slice (not nil).
	installLookupHook = func(_ context.Context, _ string) bool { return false }
	got = AllInstalled(context.Background())
	if len(got) != 0 {
		t.Errorf("none-installed scenario: got %v, want empty", agentIDs(got))
	}
}

func TestExecutableCandidates_PathOrderAndDedup(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	writeExecutable(t, filepath.Join(dir1, "claude"))
	writeExecutable(t, filepath.Join(dir2, "claude"))
	pathEnv := strings.Join([]string{dir1, dir2, dir1}, string(os.PathListSeparator))

	got := ExecutableCandidates("claude", pathEnv)
	want := []string{filepath.Join(dir1, "claude"), filepath.Join(dir2, "claude")}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLaunchCmd_ConfiguredCommands(t *testing.T) {
	commands := Commands{
		Claude:      "/Users/me/.nvm/versions/node/bin/claude",
		Codex:       "/Users/me/.nvm/versions/node/bin/codex",
		Antigravity: "/Users/me/.nvm/versions/node/bin/agy",
		Cursor:      "/Users/me/.local/bin/cursor-agent",
	}
	tests := []struct {
		name string
		id   ID
		want string
	}{
		{
			name: "claude",
			id:   IDClaude,
			want: "/Users/me/.nvm/versions/node/bin/claude --continue || /Users/me/.nvm/versions/node/bin/claude || zsh || bash || sh",
		},
		{
			name: "codex",
			id:   IDCodex,
			want: "/Users/me/.nvm/versions/node/bin/codex --continue || /Users/me/.nvm/versions/node/bin/codex || zsh || bash || sh",
		},
		{
			name: "antigravity",
			id:   IDAntigravity,
			want: "/Users/me/.nvm/versions/node/bin/agy --continue || /Users/me/.nvm/versions/node/bin/agy || zsh || bash || sh",
		},
		{
			name: "cursor",
			id:   IDCursor,
			want: "/Users/me/.local/bin/cursor-agent resume || /Users/me/.local/bin/cursor-agent || zsh || bash || sh",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LaunchCmd(tt.id, true, commands); got != tt.want {
				t.Errorf("LaunchCmd configured = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLaunchCmd_ConfiguredCommandWithSpacesIsQuoted(t *testing.T) {
	cmd := LaunchCmd(IDClaude, false, Commands{Claude: "/Users/me/Tools With Spaces/claude"})
	want := "'/Users/me/Tools With Spaces/claude'"
	if cmd != want {
		t.Errorf("LaunchCmd quoted = %q, want %q", cmd, want)
	}
}

func TestResumeArgs_ConfiguredCommands(t *testing.T) {
	commands := Commands{
		Claude:      "/tmp/claude",
		Codex:       "/tmp/codex",
		Antigravity: "/tmp/agy",
		Cursor:      "/tmp/cursor-agent",
	}
	tests := []struct {
		name string
		id   ID
		want []string
	}{
		{name: "claude", id: IDClaude, want: []string{"/tmp/claude", "--resume", "abc-123"}},
		{name: "codex", id: IDCodex, want: []string{"/tmp/codex", "resume", "abc-123"}},
		{name: "antigravity", id: IDAntigravity, want: []string{"/tmp/agy", "--conversation", "abc-123"}},
		{name: "cursor", id: IDCursor, want: []string{"/tmp/cursor-agent", "--resume", "abc-123"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResumeArgs(tt.id, "abc-123", commands)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("arg[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func agentIDs(as []Agent) []ID {
	out := make([]ID, len(as))
	for i, a := range as {
		out[i] = a.ID()
	}
	return out
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
