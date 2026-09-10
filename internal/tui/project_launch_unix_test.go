//go:build !windows

package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
)

// Execute both launch paths with a failing agent and controlled fallback
// shells. A startup failure must reach a shell without trying to resume;
// a successful agent exit must not launch a fallback.
func TestProjectLaunchShellFallback(t *testing.T) {
	for _, pathFlavor := range []bool{false, true} {
		for _, fallback := range []string{"none", "zsh", "bash", "sh"} {
			t.Run(fmt.Sprintf("path=%t/fallback=%s", pathFlavor, fallback), func(t *testing.T) {
				dir := t.TempDir()
				writeExecutable := func(name, script string) string {
					t.Helper()
					path := filepath.Join(dir, name)
					if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
						t.Fatal(err)
					}
					return path
				}
				exitCode := 23
				if fallback == "none" {
					exitCode = 0
				}
				binary := writeExecutable("agent with spaces", fmt.Sprintf("printf 'agent:%%s\\n' \"$#\"\nexit %d\n", exitCode))
				for _, shell := range []string{"zsh", "bash", "sh"} {
					script := "exit 127\n"
					if shell == fallback || fallback == "none" {
						script = fmt.Sprintf("printf '%s\\n'\n", shell)
					}
					writeExecutable(shell, script)
				}
				if err := os.MkdirAll(filepath.Join(dir, ".ccmux"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := project.SetAgent(dir, agent.IDClaude); err != nil {
					t.Fatal(err)
				}
				commands := agent.Commands{Claude: binary}
				launch := launchCmdForProjectWithCommands(project.Project{Agent: agent.IDClaude}, commands)
				if pathFlavor {
					launch = launchCmdForProjectPathWithCommands(dir, commands)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "/bin/sh", "-c", launch)
				cmd.Env = append(os.Environ(), "PATH="+dir)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("launch failed: %v\n%s", err, out)
				}
				want := "agent:0\n"
				if fallback != "none" {
					want += fallback + "\n"
				}
				if string(out) != want {
					t.Fatalf("launch output = %q, want %q", out, want)
				}
			})
		}
	}
}
