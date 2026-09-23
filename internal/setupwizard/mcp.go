package setupwizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/claudeconfig"
	"github.com/skzv/ccmux/internal/configfile"
)

// ccmuxMCPName is the key ccmux registers its MCP server under.
const ccmuxMCPName = "ccmux"

// claudeCLITimeout bounds each `claude mcp …` invocation so a wedged
// CLI can't hang `ccmux setup` / `ccmux mcp register`.
const claudeCLITimeout = 30 * time.Second

// Claude Code keeps user-scope MCP servers in the top-level
// `mcpServers` object of ~/.claude.json — where `claude mcp add
// --scope user` writes and what Claude Code loads. The `mcpServers` key
// in ~/.claude/settings.json, where older ccmux registered itself, is
// never read, so that registration silently did nothing.
//
// Registration therefore goes through the Claude CLI
// (`claude mcp add-json --scope user`) when it is on PATH — Claude Code
// owns that file and rewrites it constantly — and falls back to editing
// ~/.claude.json directly (backup first, every other key preserved
// verbatim, atomic write) when it isn't.

// lookClaudeCLI reports whether the `claude` binary is on PATH. A seam
// so tests control which registration path runs.
var lookClaudeCLI = func() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// runClaudeMCP runs `claude <args…>` with a timeout and returns its
// combined output. A seam so tests never execute a real Claude CLI.
var runClaudeMCP = func(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeCLITimeout)
	defer cancel()
	return exec.CommandContext(ctx, "claude", args...).CombinedOutput()
}

// claudeUserConfigPath returns Claude Code's user config file:
// $CLAUDE_CONFIG_DIR/.claude.json when that override is set, else
// ~/.claude.json.
func claudeUserConfigPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude.json"), nil
}

// claudeCodeInstalled reports whether there's a Claude Code to register
// with: the CLI on PATH, or the user config file it creates on first
// run (e.g. a native install whose `claude` lives in a shell alias).
func claudeCodeInstalled() bool {
	if lookClaudeCLI() {
		return true
	}
	p, err := claudeUserConfigPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// userMCPConfig is ~/.claude.json read for an mcpServers edit. top keeps
// every top-level key as raw JSON and servers every MCP entry, so a
// write re-emits everything ccmux didn't touch verbatim.
type userMCPConfig struct {
	path    string
	exists  bool
	top     map[string]json.RawMessage
	servers map[string]json.RawMessage
}

// readUserMCPConfig loads Claude Code's user config. A missing file is
// an empty config, not an error.
func readUserMCPConfig() (*userMCPConfig, error) {
	p, err := claudeUserConfigPath()
	if err != nil {
		return nil, err
	}
	c := &userMCPConfig{path: p, top: map[string]json.RawMessage{}, servers: map[string]json.RawMessage{}}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	c.exists = true
	if len(strings.TrimSpace(string(b))) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(b, &c.top); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if c.top == nil { // the file held JSON null
		c.top = map[string]json.RawMessage{}
	}
	if raw, ok := c.top["mcpServers"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &c.servers); err != nil {
			return nil, fmt.Errorf("parse %s mcpServers: %w", p, err)
		}
		if c.servers == nil {
			c.servers = map[string]json.RawMessage{}
		}
	}
	return c, nil
}

// entry decodes the named MCP server entry, reporting whether it exists.
func (c *userMCPConfig) entry(name string) (claudeconfig.MCPServer, bool) {
	raw, ok := c.servers[name]
	if !ok {
		return claudeconfig.MCPServer{}, false
	}
	var s claudeconfig.MCPServer
	// An undecodable entry still counts as present (with no args) so
	// it's replaced rather than duplicated.
	_ = json.Unmarshal(raw, &s)
	return s, true
}

// setEntry adds or replaces the named MCP server entry.
func (c *userMCPConfig) setEntry(name string, entry json.RawMessage) error {
	c.servers[name] = entry
	servers, err := json.Marshal(c.servers)
	if err != nil {
		return err
	}
	c.top["mcpServers"] = servers
	return nil
}

// save atomically replaces the file, keeping its permission bits (new
// files are 0600: the file carries account details).
func (c *userMCPConfig) save() error {
	data, err := json.MarshalIndent(c.top, "", "  ")
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(c.path); err == nil {
		mode = info.Mode().Perm()
	}
	return configfile.WriteAtomic(c.path, append(data, '\n'), mode)
}

// backupUserConfig copies Claude Code's user config into
// ~/.claude/backups/ (the same place claudeconfig keeps settings.json
// backups). A missing file needs no backup and returns "".
func backupUserConfig() (string, error) {
	p, err := claudeUserConfigPath()
	if err != nil {
		return "", err
	}
	locs, err := claudeconfig.Paths()
	if err != nil {
		return "", err
	}
	backup, err := configfile.Backup(p, locs.BackupsDir)
	if err != nil {
		return "", fmt.Errorf("back up %s: %w", p, err)
	}
	return backup, nil
}

// ccmuxMCPEntry is the JSON for the ccmux entry — the shape `claude mcp
// add-json` takes and Claude Code stores. args is always an array.
func ccmuxMCPEntry(allowMutate bool) json.RawMessage {
	args := []string{}
	if allowMutate {
		args = []string{"--allow-mutate"}
	}
	b, _ := json.Marshal(struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}{"stdio", "ccmux-mcp", args})
	return b
}

// registerCCMUXMCP writes the ccmux entry into Claude Code's user-scope
// MCP config, replacing an existing one when replace is set. It prefers
// the Claude CLI and falls back to editing ~/.claude.json when the CLI
// is missing, fails, or doesn't leave the entry where Claude Code (and
// MCPStatus) read it. The file is backed up first either way. Returns
// how it registered and the backup path.
func registerCCMUXMCP(ctx context.Context, allowMutate, replace bool) (via, backup string, err error) {
	entry := ccmuxMCPEntry(allowMutate)
	if backup, err = backupUserConfig(); err != nil {
		return "", "", err
	}
	var cliErr error
	if lookClaudeCLI() {
		if replace {
			// add-json refuses to overwrite; drop the old entry first.
			// A failure here surfaces through add-json below.
			_, _ = runClaudeMCP(ctx, "mcp", "remove", "--scope", "user", ccmuxMCPName)
		}
		out, err := runClaudeMCP(ctx, "mcp", "add-json", "--scope", "user", ccmuxMCPName, string(entry))
		if err == nil {
			if mode, ok, serr := MCPStatus(); serr == nil && ok && (mode == mcpModeMutate) == allowMutate {
				return "claude mcp add-json --scope user", backup, nil
			}
			cliErr = errors.New("claude mcp add-json succeeded but the entry isn't in " + displayUserConfigPath())
		} else {
			cliErr = fmt.Errorf("claude mcp add-json: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	cfg, err := readUserMCPConfig()
	if err != nil {
		return "", backup, joinErrs(cliErr, err)
	}
	if err := cfg.setEntry(ccmuxMCPName, entry); err != nil {
		return "", backup, joinErrs(cliErr, err)
	}
	if err := cfg.save(); err != nil {
		return "", backup, joinErrs(cliErr, fmt.Errorf("write %s: %w", cfg.path, err))
	}
	via = "edited " + displayUserConfigPath()
	if cliErr != nil {
		via += " (" + cliErr.Error() + ")"
	}
	return via, backup, nil
}

func joinErrs(a, b error) error {
	if a == nil {
		return b
	}
	return errors.Join(b, a)
}

// displayUserConfigPath is the user config path for messages, with the
// home directory collapsed to ~.
func displayUserConfigPath() string {
	p, err := claudeUserConfigPath()
	if err != nil {
		return "~/.claude.json"
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// removeStaleSettingsEntry deletes the `mcpServers.ccmux` entry older
// ccmux wrote into ~/.claude/settings.json (a key Claude Code never
// reads), via the claudeconfig round-trip so every other setting and
// server survives and a backup is kept. Only an entry pointing at
// ccmux-mcp is removed. Reports whether it removed one.
func removeStaleSettingsEntry() (bool, error) {
	s, err := claudeconfig.ReadSettings()
	if err != nil {
		return false, err
	}
	stale, ok := s.MCPServers[ccmuxMCPName]
	if !ok || filepath.Base(stale.Command) != "ccmux-mcp" {
		return false, nil
	}
	delete(s.MCPServers, ccmuxMCPName)
	if _, err := claudeconfig.WriteSettings(s); err != nil {
		return false, err
	}
	return true, nil
}

// reportStaleSettingsCleanup runs removeStaleSettingsEntry and prints
// the outcome; failures are warnings, never fatal.
func reportStaleSettingsCleanup(out io.Writer, prefix string) {
	removed, err := removeStaleSettingsEntry()
	switch {
	case err != nil:
		fmt.Fprintf(out, "%scouldn't remove the old ccmux entry from ~/.claude/settings.json: %v\n", prefix, err)
	case removed:
		fmt.Fprintf(out, "%sremoved the old, unused ccmux entry from ~/.claude/settings.json\n", prefix)
	}
}

// stepMCP offers to register ccmux-mcp as a user-scope MCP server in
// Claude Code. Idempotent: re-runs detect an already-registered entry
// and report it without touching anything.
//
// The wizard's role here is discoverability — the user could equally
// well run `claude mcp add`. Most people don't know the MCP server
// exists when they first install ccmux, and asking once during setup
// is the cheapest way to surface it.
func stepMCP(ctx context.Context, out io.Writer) error {
	// No point asking to wire ccmux into a Claude Code that isn't there.
	if !claudeCodeInstalled() {
		fmt.Fprintln(out, stMuted.Render("  Claude Code not on PATH — skipping (install it, then re-run setup)"))
		return nil
	}

	cfg, err := readUserMCPConfig()
	if err != nil {
		// Real failure (permissions, malformed JSON). Surface it but
		// don't bail the wizard — the rest of the steps still matter.
		fmt.Fprintf(out, "  %s couldn't read %s: %v\n", stWarn.Render("⚠"), displayUserConfigPath(), err)
		return nil
	}
	reportStaleSettingsCleanup(out, "  "+stMuted.Render("•")+" ")

	if existing, ok := cfg.entry(ccmuxMCPName); ok {
		// Already wired up. Report what mode it's in so the user can
		// see whether `--allow-mutate` is on without opening the file.
		fmt.Fprintf(out, "  %s ccmux-mcp is already wired into Claude Code (%s)\n", stOK.Render("✓"), mcpMode(existing))
		fmt.Fprintln(out, stMuted.Render("    run `ccmux mcp register [--allow-mutate]` to change the mode"))
		return nil
	}

	fmt.Fprintln(out, stMuted.Render("  ccmux ships an MCP server (ccmux-mcp) that lets coding agents see and act"))
	fmt.Fprintln(out, stMuted.Render("  on every ccmux session, project, conversation, and tailnet peer."))

	register, err := confirm(ctx, true,
		"Wire ccmux-mcp into Claude Code?",
		"Registers a user-scope `ccmux` MCP server (`claude mcp add-json --scope user`, or an edit of ~/.claude.json with a backup in ~/.claude/backups/). Existing servers are preserved.",
		"Yes, register",
		"No, skip")
	if err != nil {
		return err
	}
	if !register {
		fmt.Fprintln(out, stMuted.Render("  skipped — register later with `ccmux mcp register`"))
		return nil
	}

	allowMutate, err := confirm(ctx, false,
		"Also enable the mutating tools?",
		"spawn_session / send_keys / kill_session — lets Claude type into existing sessions, start new ones, and kill them. Safe default is off (read-only); switch later with `ccmux mcp register --allow-mutate`.",
		"Yes, enable mutating tools",
		"No, keep it read-only")
	if err != nil {
		return err
	}

	via, backup, err := registerCCMUXMCP(ctx, allowMutate, false)
	if err != nil {
		return fmt.Errorf("register ccmux-mcp with Claude Code: %w", err)
	}

	label := mcpModeReadOnly
	if allowMutate {
		label = "with --allow-mutate (spawn / send-keys / kill enabled)"
	}
	fmt.Fprintf(out, "  %s wired ccmux-mcp into Claude Code (%s)\n", stOK.Render("✓"), label)
	fmt.Fprintf(out, "  %s %s\n", stMuted.Render("•"), via)
	if backup != "" {
		fmt.Fprintf(out, "  %s backup at %s\n", stMuted.Render("•"), backup)
	}
	fmt.Fprintln(out, stMuted.Render("    restart Claude Code so the new MCP server registration takes effect"))
	return nil
}

// Mode labels shared by the wizard, `ccmux mcp register` and `ccmux mcp
// status`.
const (
	mcpModeReadOnly = "read-only"
	mcpModeMutate   = "with --allow-mutate"
)

// mcpMode labels an entry by whether it passes --allow-mutate.
func mcpMode(s claudeconfig.MCPServer) string {
	if containsArg(s.Args, "--allow-mutate") {
		return mcpModeMutate
	}
	return mcpModeReadOnly
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
// if the entry already exists with the same mode nothing is touched and
// the report says so; a different mode replaces the entry.
//
// Lives in this package rather than cmd/ccmux/cmd so the registration
// logic stays alongside the wizard step that shares it.
func RegisterMCPForCLI(ctx context.Context, out io.Writer, allowMutate bool) error {
	if !claudeCodeInstalled() {
		fmt.Fprintln(out, "✗ Claude Code not found (no `claude` on PATH, no ~/.claude.json) — install it first, then re-run")
		return nil
	}

	cfg, err := readUserMCPConfig()
	if err != nil {
		return fmt.Errorf("read %s: %w", displayUserConfigPath(), err)
	}
	reportStaleSettingsCleanup(out, "  ")

	want := mcpModeReadOnly
	if allowMutate {
		want = mcpModeMutate
	}
	existing, ok := cfg.entry(ccmuxMCPName)
	if ok && mcpMode(existing) == want {
		fmt.Fprintf(out, "✓ ccmux-mcp already registered (%s) — nothing to do\n", want)
		return nil
	}

	via, backup, err := registerCCMUXMCP(ctx, allowMutate, ok)
	if err != nil {
		return fmt.Errorf("register ccmux-mcp with Claude Code: %w", err)
	}

	mode := mcpModeReadOnly
	if allowMutate {
		mode = "with --allow-mutate (spawn / send-keys / kill enabled)"
	}
	fmt.Fprintf(out, "✓ ccmux-mcp registered with Claude Code (%s)\n", mode)
	fmt.Fprintf(out, "  via: %s\n", via)
	if backup != "" {
		fmt.Fprintf(out, "  backup: %s\n", backup)
	}
	fmt.Fprintln(out, "  restart Claude Code so the new MCP server takes effect")
	return nil
}

// MCPStatus reports whether ccmux-mcp is registered as a user-scope MCP
// server in Claude Code's user config (~/.claude.json, the file Claude
// Code reads), and in what mode. Powers `ccmux mcp status`. Returns
// ("", false, nil) when not registered; ("", false, err) on I/O or
// parse failure.
func MCPStatus() (mode string, registered bool, err error) {
	cfg, err := readUserMCPConfig()
	if err != nil {
		return "", false, err
	}
	existing, ok := cfg.entry(ccmuxMCPName)
	if !ok {
		return "", false, nil
	}
	return mcpMode(existing), true, nil
}

// MCPUserConfigPath is the file MCPStatus reads, for CLI messages.
func MCPUserConfigPath() string { return displayUserConfigPath() }
