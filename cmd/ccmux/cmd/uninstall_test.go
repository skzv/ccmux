package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/daemonservice"
)

// TestUninstallServiceStep_RoutesFailureThroughReport — regression for
// `ccmux uninstall` swallowing a daemonservice.Uninstall() failure: the
// error branch did nothing, so the command printed "Uninstall
// complete." and exited 0 while launchd's KeepAlive kept respawning a
// deleted binary. The step must hand the error to report() so it
// prints ✗ and sets the command's non-zero exit.
func TestUninstallServiceStep_RoutesFailureThroughReport(t *testing.T) {
	boom := errors.New("launchctl bootout failed")
	var gotMsg string
	var gotErr error
	calls := 0
	uninstallServiceStep(
		func() (daemonservice.Status, error) { return daemonservice.Status{}, boom },
		func(msg string, err error) {
			calls++
			gotMsg, gotErr = msg, err
		},
	)
	if calls != 1 {
		t.Fatalf("report called %d times, want 1", calls)
	}
	if !errors.Is(gotErr, boom) {
		t.Errorf("report err = %v, want the uninstall failure", gotErr)
	}
	if gotMsg == "" {
		t.Error("report msg empty")
	}
}

// TestUninstallServiceStep_SuccessReportsOK — the happy path still
// reports a nil-error line.
func TestUninstallServiceStep_SuccessReportsOK(t *testing.T) {
	var gotErr error
	calls := 0
	uninstallServiceStep(
		func() (daemonservice.Status, error) { return daemonservice.Status{}, nil },
		func(msg string, err error) {
			calls++
			gotErr = err
		},
	)
	if calls != 1 {
		t.Fatalf("report called %d times, want 1", calls)
	}
	if gotErr != nil {
		t.Errorf("report err = %v, want nil on success", gotErr)
	}
}

// TestBuildUninstallPlan_UnregistersMCP — uninstall removed ccmux-mcp
// but left its registration in ~/.claude.json, so Claude Code reported
// a failing MCP server on every start. A registered ccmux entry must be
// in the plan (and flagged for the run); without one it isn't.
func TestBuildUninstallPlan_UnregistersMCP(t *testing.T) {
	home := isolateHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	plan, err := buildUninstallPlan(false, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.unregisterMCP {
		t.Error("plan unregisters ccmux-mcp although nothing is registered")
	}

	body := `{"mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp", "args": []}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err = buildUninstallPlan(false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.unregisterMCP {
		t.Error("plan doesn't unregister the ccmux MCP server")
	}
	if !strings.Contains(strings.Join(plan.steps, "\n"), "unregister the ccmux MCP server") {
		t.Errorf("plan output doesn't list the MCP step:\n%s", strings.Join(plan.steps, "\n"))
	}
}

// TestUninstallMCPStep_ReportsOutcome — the step's summary and its
// failure both reach report(), so a failed unregister prints ✗ and
// fails the command instead of vanishing.
func TestUninstallMCPStep_ReportsOutcome(t *testing.T) {
	var msgs []string
	var errs []error
	report := func(msg string, err error) { msgs, errs = append(msgs, msg), append(errs, err) }

	uninstallMCPStep(func(context.Context) (string, error) { return "unregistered ccmux-mcp", nil }, report)
	boom := errors.New("parse ~/.claude.json: unexpected EOF")
	uninstallMCPStep(func(context.Context) (string, error) { return "", boom }, report)

	if len(msgs) != 2 {
		t.Fatalf("report called %d times, want 2", len(msgs))
	}
	if msgs[0] != "unregistered ccmux-mcp" || errs[0] != nil {
		t.Errorf("success reported as (%q, %v)", msgs[0], errs[0])
	}
	if !errors.Is(errs[1], boom) {
		t.Errorf("failure reported as (%q, %v), want the unregister error", msgs[1], errs[1])
	}
}
