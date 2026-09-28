package setupwizard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// foreignEntry is someone else's MCP server that happens to be named
// "ccmux".
const foreignEntry = `{"mcpServers": {"ccmux": {"type": "stdio", "command": "/opt/tools/my-ccmux-bridge"}}}`

// TestMCPStatus_ForeignEntryIsNotRegistered — status reported any
// entry named "ccmux" as ccmux-mcp, even one running another command.
func TestMCPStatus_ForeignEntryIsNotRegistered(t *testing.T) {
	home := withFakeClaudeHome(t)
	seedUserConfig(t, home, foreignEntry)

	if mode, ok, err := MCPStatus(); err != nil || ok {
		t.Errorf("MCPStatus = (%q, %v, %v), want not registered", mode, ok, err)
	}
	st, err := MCPRegistrationStatus()
	if err != nil || !st.Present || st.Ours || st.Command != "/opt/tools/my-ccmux-bridge" {
		t.Errorf("MCPRegistrationStatus = %+v, %v; want a present, foreign entry", st, err)
	}
}

// TestRegisterMCP_RefusesForeignEntry — `ccmux mcp register` said
// "already registered" over a foreign entry (same mode) and silently
// overwrote it with --allow-mutate. Without --force it must refuse and
// leave the file untouched.
func TestRegisterMCP_RefusesForeignEntry(t *testing.T) {
	for _, allowMutate := range []bool{false, true} {
		home := withFakeClaudeHome(t)
		calls := fakeClaudeCLI(t, home)
		seedUserConfig(t, home, foreignEntry)

		var buf bytes.Buffer
		err := RegisterMCP(context.Background(), &buf, RegisterOptions{AllowMutate: allowMutate})
		if !errors.Is(err, ErrForeignMCPEntry) {
			t.Errorf("allowMutate=%v: err = %v, want ErrForeignMCPEntry", allowMutate, err)
		}
		if err != nil && !strings.Contains(err.Error(), "--force") {
			t.Errorf("allowMutate=%v: error should point at --force: %v", allowMutate, err)
		}
		if strings.Contains(buf.String(), "already registered") {
			t.Errorf("allowMutate=%v: reported a foreign entry as registered:\n%s", allowMutate, buf.String())
		}
		if len(*calls) != 0 {
			t.Errorf("allowMutate=%v: claude ran: %q", allowMutate, *calls)
		}
		if raw, _ := os.ReadFile(filepath.Join(home, ".claude.json")); string(raw) != foreignEntry {
			t.Errorf("allowMutate=%v: ~/.claude.json changed:\n%s", allowMutate, raw)
		}
	}
}

// TestRegisterMCP_ForceReplacesForeignEntry — with --force the foreign
// entry is replaced by ccmux-mcp (backup first).
func TestRegisterMCP_ForceReplacesForeignEntry(t *testing.T) {
	home := withFakeClaudeHome(t)
	noClaudeCLI(t)
	seedUserConfig(t, home, foreignEntry)

	var buf bytes.Buffer
	if err := RegisterMCP(context.Background(), &buf, RegisterOptions{AllowMutate: true, Force: true}); err != nil {
		t.Fatalf("RegisterMCP --force: %v\n%s", err, buf.String())
	}
	if servers := readUserServers(t, home); servers["ccmux"]["command"] != "ccmux-mcp" {
		t.Errorf("ccmux entry = %v, want ccmux-mcp", servers["ccmux"])
	}
	if mode, ok, _ := MCPStatus(); !ok || mode != mcpModeMutate {
		t.Errorf("MCPStatus = (%q, %v), want registered %s", mode, ok, mcpModeMutate)
	}
	if !strings.Contains(buf.String(), "my-ccmux-bridge") {
		t.Errorf("output should say which server it replaced:\n%s", buf.String())
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".claude", "backups")); len(entries) == 0 {
		t.Error("replaced a foreign entry without a backup")
	}
}

// TestStepMCP_LeavesForeignEntryAlone — the wizard must not report a
// foreign entry as "already wired", nor offer to overwrite it.
func TestStepMCP_LeavesForeignEntryAlone(t *testing.T) {
	home := withFakeClaudeHome(t)
	calls := fakeClaudeCLI(t, home)
	seedUserConfig(t, home, foreignEntry)

	var buf bytes.Buffer
	if err := stepMCP(withAssumeYes(context.Background()), &buf); err != nil {
		t.Fatalf("stepMCP: %v", err)
	}
	if strings.Contains(buf.String(), "already wired") {
		t.Errorf("wizard called a foreign entry ccmux-mcp:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "my-ccmux-bridge") {
		t.Errorf("wizard should name the foreign server:\n%s", buf.String())
	}
	if len(*calls) != 0 {
		t.Errorf("claude ran: %q", *calls)
	}
	if raw, _ := os.ReadFile(filepath.Join(home, ".claude.json")); string(raw) != foreignEntry {
		t.Errorf("~/.claude.json changed:\n%s", raw)
	}
}
