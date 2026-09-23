package agentdetect

import (
	"strings"
	"testing"
)

// scrollback returns n lines of ordinary agent output, standing in for
// the transcript that sits above an agent's footer in a 60-line capture.
func scrollback(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "  output line of the transcript"
	}
	return strings.Join(lines, "\n")
}

// TestOpenCodeWorkingFooter_MatchesTokensNotPaths — OpenCode's footer
// prints the cwd; the old `contains "working"` rule matched it, so a
// session in `…/networking-lab` or `~/Working/…` stayed "active"
// forever (no bell, sleep lock never released). Only the real working
// tokens may match.
func TestOpenCodeWorkingFooter_MatchesTokensNotPaths(t *testing.T) {
	for _, id := range []ID{"opencode", "kilo"} {
		cases := []struct {
			name    string
			footer  string
			working bool
		}{
			{"cwd containing networking", "tab agents  ctrl+p commands\n  ~/scratch/            1.16.2\n  networking-lab", false},
			{"cwd under ~/Working", "tab agents  ctrl+p commands\n  ~/Working/api            1.16.2", false},
			{"word working in prose", "the networking stack is working fine", false},
			{"1.x interrupt hint", "■■⬝⬝  esc interrupt        tab agents  ctrl+p commands\n  ~/Projects/api   1.16.2", true},
			{"legacy interrupt hint", "generating code…\n(esc to interrupt)", true},
			{"second press", "esc again to interrupt\n  ~/Projects/api   1.16.2", true},
			{"busy text", "  Working...\n  ~/Projects/api   1.16.2", true},
		}
		for _, tc := range cases {
			t.Run(string(id)+"/"+tc.name, func(t *testing.T) {
				res, err := ClassifyAgent(id, Input{Pane: scrollback(10) + "\n" + tc.footer})
				if err != nil {
					t.Fatal(err)
				}
				if got := res.MatchedRuleID == "body_working_footer"; got != tc.working {
					t.Errorf("body_working_footer matched=%v, want %v (result %+v)", got, tc.working, res)
				}
			})
		}
	}
}

// TestBlockedRules_ScrollbackDoesNotBeatWorkingFooter — the kiro /
// qoder / amp / opencode / kilo blocked rules scanned the whole capture
// at priority 900, above each agent's working footer (800). Benign
// scrollback mentioning an approval pinned needs_input — and rang the
// bell instantly — while the agent was visibly working. Scrollback far
// above the footer must now leave the working footer in charge.
func TestBlockedRules_ScrollbackDoesNotBeatWorkingFooter(t *testing.T) {
	cases := []struct {
		agent   ID
		phrase  string
		footer  string
		working string
	}{
		{"kiro", "This step requires approval from the security team.", "  ◔ kiro is working", "body_working_marker"},
		{"qoder", "Permission required for writes outside the repo; enter your response in the doc.", "thinking…\n(esc to cancel, ctrl+c to quit)", "body_working_footer"},
		{"amp", "Next we invoke tool X, then wait for approval.", "running tests…\nesc to cancel", "body_working_footer"},
		{"opencode", "△ Permission required was the old dialog title.", "■■⬝⬝  esc interrupt", "body_working_footer"},
		{"kilo", "△ Permission required was the old dialog title.", "■■⬝⬝  esc interrupt", "body_working_footer"},
	}
	for _, tc := range cases {
		t.Run(string(tc.agent), func(t *testing.T) {
			pane := tc.phrase + "\n" + scrollback(20) + "\n" + tc.footer
			res, err := ClassifyAgent(tc.agent, Input{Pane: pane})
			if err != nil {
				t.Fatal(err)
			}
			if res.MatchedRuleID != tc.working || res.State != StateActive {
				t.Errorf("got %+v, want the working rule %q (state active)", res, tc.working)
			}
		})
	}
}

// TestBlockedFooterRules_AreIdleGated — the blocked rules that used to
// scan the whole pane still fire on a real footer-area prompt, but
// carry require_idle so the caller holds them at active until the pane
// has gone quiet (a prompt-shaped line flashing by mid-turn is not a
// reason to page the user).
func TestBlockedFooterRules_AreIdleGated(t *testing.T) {
	cases := []struct {
		agent ID
		pane  string
	}{
		{"kiro", "This action requires approval.\n  yes, single permission\n  trust, always allow\n  no"},
		{"qoder", "Qoder is waiting for user confirmation\n  enter your response"},
		{"amp", "Waiting for approval…\nAllow editing file: src/main.go?"},
		{"opencode", "△ Permission required\n  ⚙ Call tool bash\n\n  Allow once   Allow always   Reject\n  ⇆ select  enter confirm"},
		{"opencode", "  Allow once   Allow always   Reject\n  ⇆ select  enter confirm\n  ~/Projects/api   1.16.2"},
		{"kilo", "△ Permission required\n  Allow once   Allow always   Reject"},
	}
	for _, tc := range cases {
		t.Run(string(tc.agent), func(t *testing.T) {
			res, err := ClassifyAgent(tc.agent, Input{Pane: scrollback(20) + "\n" + tc.pane})
			if err != nil {
				t.Fatal(err)
			}
			if res.State != StateNeedsInput {
				t.Fatalf("got %+v, want needs_input", res)
			}
			if !res.RequireIdle {
				t.Errorf("rule %q must carry require_idle", res.MatchedRuleID)
			}
		})
	}
}

// TestClaudeShellPrompt_IgnoresPercentFooter — claude_shell_prompt
// matched any last line ending in `$`/`#`/`%`, so Claude's own footer
// (`Context left until auto-compact: 7%`) or a statusline (`ctx 42%`)
// turned a healthy session red "error". A tail ending in `%` only means
// a shell prompt when no Claude chrome is on screen.
func TestClaudeShellPrompt_IgnoresPercentFooter(t *testing.T) {
	rule := strings.Repeat("─", 80)
	cases := []struct {
		name  string
		pane  string
		error bool
	}{
		{"v2 context footer", "done\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ auto mode on (shift+tab to cycle)     Context left until auto-compact: 7%", false},
		{"v2 statusline", "done\n" + rule + "\n❯ \n" + rule + "\n  ? for shortcuts\n  opus · ~/Projects/ccmux · ctx 42%", false},
		{"v1 frame with footer", "done\n╭──────────╮\n│ >        │\n╰──────────╯\n  ? for shortcuts   Context left until auto-compact: 7%", false},
		{"zsh prompt", "Error: Cannot find module 'cli.js'\n\nNode.js v22.22.3\nuser@host ~ % ", true},
		{"bash prompt", "Segmentation fault\nsasha@laptop:~/projects/foo$", true},
		{"root prompt", "killed\nroot@host:/#", true},
	}
	var shellRule []Rule
	for _, r := range RulesFor("claude") {
		if r.ID == "claude_shell_prompt" {
			shellRule = append(shellRule, r)
		}
	}
	if len(shellRule) != 1 {
		t.Fatal("claude_shell_prompt rule not found")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The rule on its own must not fire — not merely lose on
			// priority to a prompt rule that happens to match too.
			if got := Evaluate(shellRule, Input{Pane: tc.pane}).MatchedRuleID != ""; got != tc.error {
				t.Errorf("claude_shell_prompt matched=%v, want %v", got, tc.error)
			}
			res, err := ClassifyAgent("claude", Input{Pane: tc.pane})
			if err != nil {
				t.Fatal(err)
			}
			if got := res.State == StateError; got != tc.error {
				t.Errorf("state %q (rule %q), want error=%v", res.State, res.MatchedRuleID, tc.error)
			}
		})
	}
}

// TestClaudeV2Rules — Claude Code v2 draws its input box as a `❯` line
// between two `────` rules, above a footer; the v1 rounded-corner rule
// never matched it, so needs_input never fired on current Claude Code.
func TestClaudeV2Rules(t *testing.T) {
	rule := strings.Repeat("─", 100)
	cases := []struct {
		name   string
		pane   string
		wantID string
	}{
		{"empty input box", "out\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents", "claude_prompt_frame_v2"},
		{"box without trailing space", "out\n" + rule + "\n❯\n" + rule + "\n  ? for shortcuts", "claude_prompt_frame_v2"},
		{"typed multi-line input", "out\n" + rule + "\n❯ one\n  two\n  three\n" + rule + "\n  ? for shortcuts", "claude_prompt_frame_v2"},
		{"working, box still drawn", "✻ Cogitating… (12s · esc to interrupt)\n" + rule + "\n❯ \n" + rule + "\n  ? for shortcuts", "claude_prompt_frame_v2"},
		{"permission prompt", "out\n" + rule + "\n Bash command\n   ls\n Do you want to proceed?\n ❯ 1. Yes\n   2. No", "claude_dialog_v2"},
		{"permission cursor moved", "out\n" + rule + "\n Edit file\n   1. Yes\n ❯ 2. Yes, allow all edits\n   3. No", "claude_dialog_v2"},
		{"trust dialog", "out\n Accessing workspace:\n ❯ No, exit\n   Yes, I trust this folder\n Enter to confirm · Esc to cancel", "claude_dialog_v2"},
		{"rule lines without a prompt", "out\n" + rule + "\n plain text\n" + rule, ""},
		{"prompt glyph without rules", "~/Projects/api main\n❯ ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ClassifyAgent("claude", Input{Pane: tc.pane})
			if err != nil {
				t.Fatal(err)
			}
			if res.MatchedRuleID != tc.wantID {
				t.Fatalf("matched %q (%+v), want %q", res.MatchedRuleID, res, tc.wantID)
			}
			if tc.wantID != "" && (res.State != StateNeedsInput || !res.RequireIdle) {
				t.Errorf("%q must be blocked + require_idle, got %+v", tc.wantID, res)
			}
		})
	}
}
