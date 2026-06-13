package setupwizard

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/skzv/ccmux/internal/claudeconfig"
)

// stepMCP offers to register ccmux-mcp into Claude Code's MCP-server
// config (~/.claude/settings.json). Idempotent: re-runs detect an
// already-registered entry and report it without touching the file.
//
// The wizard's role here is discoverability — the user could equally
// well hand-edit settings.json. Most people don't know the MCP server
// exists when they first install ccmux, and asking once during setup
// is the cheapest way to surface it.
func stepMCP(ctx context.Context, out io.Writer) error {
	// First check Claude Code is actually installed — there's no point
	// asking to wire ccmux into a CLI that doesn't exist. ReadSettings
	// alone won't tell us this because it returns an empty Settings for
	// a missing file, which we WANT in the "Claude is installed but
	// hasn't written settings.json yet" case. The binary lookup
	// disambiguates: it's the cleanest signal that Claude Code is
	// available on this machine.
	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Fprintln(out, stMuted.Render("  Claude Code not on PATH — skipping (install it, then re-run setup)"))
		return nil
	}

	s, err := claudeconfig.ReadSettings()
	if err != nil {
		// Real failure (permissions, malformed JSON). Surface it but
		// don't bail the wizard — the rest of the steps still matter.
		fmt.Fprintf(out, "  %s couldn't read ~/.claude/settings.json: %v\n", stWarn.Render("⚠"), err)
		return nil
	}

	if existing, ok := s.MCPServers["ccmux"]; ok {
		// Already wired up. Report what mode it's in so the user can
		// see whether `--allow-mutate` is on without opening the file.
		label := "read-only"
		if containsArg(existing.Args, "--allow-mutate") {
			label = "with --allow-mutate"
		}
		fmt.Fprintf(out, "  %s ccmux-mcp is already wired into Claude Code (%s)\n", stOK.Render("✓"), label)
		fmt.Fprintln(out, stMuted.Render("    edit ~/.claude/settings.json or re-run after unregistering to change the mode"))
		return nil
	}

	fmt.Fprintln(out, stMuted.Render("  ccmux ships an MCP server (ccmux-mcp) that lets coding agents see and act"))
	fmt.Fprintln(out, stMuted.Render("  on every ccmux session, project, conversation, and tailnet peer."))

	register, err := confirm(ctx, true,
		"Wire ccmux-mcp into Claude Code?",
		"Adds a `ccmux` entry to ~/.claude/settings.json under `mcpServers`. Existing entries are preserved (a timestamped backup is written to ~/.claude/backups/ before the change).",
		"Yes, register",
		"No, skip")
	if err != nil {
		return err
	}
	if !register {
		fmt.Fprintln(out, stMuted.Render("  skipped — register later by re-running `ccmux setup`"))
		return nil
	}

	allowMutate, err := confirm(ctx, false,
		"Also enable the mutating tools?",
		"spawn_session / send_keys / kill_session — lets Claude type into existing sessions, start new ones, and kill them. Safe default is off (read-only); you can flip it later by editing the `args` array in settings.json.",
		"Yes, enable mutating tools",
		"No, keep it read-only")
	if err != nil {
		return err
	}

	registerCCMUXMCP(s, allowMutate)
	backup, err := claudeconfig.WriteSettings(s)
	if err != nil {
		return fmt.Errorf("write ~/.claude/settings.json: %w", err)
	}

	label := "read-only"
	if allowMutate {
		label = "with --allow-mutate (spawn / send-keys / kill enabled)"
	}
	fmt.Fprintf(out, "  %s wired ccmux-mcp into Claude Code (%s)\n", stOK.Render("✓"), label)
	if backup != "" {
		fmt.Fprintf(out, "  %s backup at %s\n", stMuted.Render("•"), backup)
	}
	fmt.Fprintln(out, stMuted.Render("    restart Claude Code so the new MCP server registration takes effect"))
	return nil
}

// registerCCMUXMCP mutates settings to add (or replace) the `ccmux`
// MCP entry. Pure: takes settings, returns nothing — tests call this
// directly without spinning up a wizard or touching disk.
//
// Always replaces any prior `ccmux` entry; the caller (stepMCP) is
// the one that checks the idempotent case. Keeping this function
// unconditional makes the testable shape simpler: one job, no
// branching on existing state.
func registerCCMUXMCP(s *claudeconfig.Settings, allowMutate bool) {
	if s.MCPServers == nil {
		s.MCPServers = map[string]claudeconfig.MCPServer{}
	}
	var args []string
	if allowMutate {
		args = []string{"--allow-mutate"}
	}
	s.MCPServers["ccmux"] = claudeconfig.MCPServer{
		Type:    "stdio",
		Command: "ccmux-mcp",
		Args:    args,
	}
}

// containsArg reports whether the args slice has `want` anywhere in
// it. Used to detect "is --allow-mutate already set" without coupling
// to argv ordering. Linear scan — args slices have at most a handful
// of entries.
func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// RegisterMCPForCLI is the public surface the `ccmux mcp register`
// subcommand calls. Same effect as the wizard step but skips all the
// prompts — the CLI takes its decision from --allow-mutate. Idempotent:
// if the entry already exists with the same mode the file is
// untouched and the report says so.
//
// Lives in this package rather than cmd/ccmux/cmd so the registration
// logic stays alongside the wizard step that shares it; the two paths
// stay in sync by sharing the same registerCCMUXMCP helper.
func RegisterMCPForCLI(_ context.Context, out io.Writer, allowMutate bool) error {
	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Fprintln(out, "✗ Claude Code not on PATH — install it first, then re-run")
		return nil
	}

	s, err := claudeconfig.ReadSettings()
	if err != nil {
		return fmt.Errorf("read ~/.claude/settings.json: %w", err)
	}

	// Idempotent: same mode → no-op + report. Different mode → replace.
	if existing, ok := s.MCPServers["ccmux"]; ok {
		hadMutate := containsArg(existing.Args, "--allow-mutate")
		if hadMutate == allowMutate {
			mode := "read-only"
			if allowMutate {
				mode = "with --allow-mutate"
			}
			fmt.Fprintf(out, "✓ ccmux-mcp already registered (%s) — nothing to do\n", mode)
			return nil
		}
		// Mode change requested. Fall through to re-register.
	}

	registerCCMUXMCP(s, allowMutate)
	backup, err := claudeconfig.WriteSettings(s)
	if err != nil {
		return fmt.Errorf("write ~/.claude/settings.json: %w", err)
	}

	mode := "read-only"
	if allowMutate {
		mode = "with --allow-mutate (spawn / send-keys / kill enabled)"
	}
	fmt.Fprintf(out, "✓ ccmux-mcp registered with Claude Code (%s)\n", mode)
	if backup != "" {
		fmt.Fprintf(out, "  backup: %s\n", backup)
	}
	fmt.Fprintln(out, "  restart Claude Code so the new MCP server takes effect")
	return nil
}

// MCPStatus reports whether ccmux-mcp is registered in the local
// Claude Code settings, and in what mode. Powers `ccmux mcp status`.
// Returns ("", false, nil) when not registered; ("", false, err) on
// I/O failure.
func MCPStatus() (mode string, registered bool, err error) {
	s, err := claudeconfig.ReadSettings()
	if err != nil {
		return "", false, err
	}
	existing, ok := s.MCPServers["ccmux"]
	if !ok {
		return "", false, nil
	}
	if containsArg(existing.Args, "--allow-mutate") {
		return "with --allow-mutate", true, nil
	}
	return "read-only", true, nil
}
