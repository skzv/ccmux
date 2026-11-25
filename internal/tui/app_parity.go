package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemonservice"
	"github.com/skzv/ccmux/internal/setupwizard"
)

type daemonActionDoneMsg struct {
	Summary string
	Err     error
}

type claudeMCPToggledMsg struct {
	Summary string
	Err     error
}

// daemonRestartFn and daemonProbeFn are test seams for the TUI's `D`
// daemon-lifecycle action.
var (
	daemonProbeFn   = daemonservice.Probe
	daemonRestartFn = daemonservice.Restart
	daemonInstallFn = daemonservice.Install
)

// mcpStatusFn, mcpRegisterFn, mcpUnregisterFn are test seams for the
// Agents → Claude `w` MCP toggle action.
var (
	mcpStatusFn     = setupwizard.MCPRegistrationStatus
	mcpRegisterFn   = setupwizard.RegisterMCP
	mcpUnregisterFn = setupwizard.UnregisterMCP
)

func (a App) addHostCmd(h config.Host) tea.Cmd {
	return func() tea.Msg {
		_, err := config.Update(func(cfg *config.Config) error {
			for _, existing := range cfg.Hosts {
				if existing.Name == h.Name {
					return fmt.Errorf("host %q already exists", h.Name)
				}
			}
			cfg.Hosts = append(cfg.Hosts, h)
			return nil
		})
		return hostAddedMsg{Name: h.Name, Err: err}
	}
}

func (a App) removeHostCmd(name string) tea.Cmd {
	return func() tea.Msg {
		_, err := config.Update(func(cfg *config.Config) error {
			out := make([]config.Host, 0, len(cfg.Hosts))
			removed := false
			for _, h := range cfg.Hosts {
				if h.Name == name {
					removed = true
					continue
				}
				out = append(out, h)
			}
			if !removed {
				return fmt.Errorf("no configured host named %q", name)
			}
			cfg.Hosts = out
			return nil
		})
		return hostRemovedMsg{Name: name, Err: err}
	}
}

func daemonLifecycleCmd() tea.Cmd {
	return func() tea.Msg {
		st := daemonProbeFn()
		if st.OS != "darwin" && st.OS != "linux" {
			return daemonActionDoneMsg{Err: fmt.Errorf("daemon service unsupported on %s", st.OS)}
		}
		if st.ServiceEnabled || st.Running {
			if _, err := daemonRestartFn(); err != nil {
				return daemonActionDoneMsg{Err: err}
			}
			return daemonActionDoneMsg{Summary: tr("ccmuxd restarted")}
		}
		if !st.BinaryInstalled {
			return daemonActionDoneMsg{Err: fmt.Errorf("ccmuxd binary not found — reinstall ccmux")}
		}
		if _, err := daemonInstallFn(); err != nil {
			return daemonActionDoneMsg{Err: err}
		}
		return daemonActionDoneMsg{Summary: tr("ccmuxd started")}
	}
}

func toggleClaudeMCPCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		st, err := mcpStatusFn()
		if err != nil {
			return claudeMCPToggledMsg{Err: err}
		}
		if st.Ours {
			summary, uerr := mcpUnregisterFn(ctx)
			if uerr != nil {
				return claudeMCPToggledMsg{Err: uerr}
			}
			if strings.TrimSpace(summary) == "" {
				summary = tr("ccmux-mcp unregistered from Claude Code")
			}
			return claudeMCPToggledMsg{Summary: summary}
		}
		if rerr := mcpRegisterFn(ctx, io.Discard, setupwizard.RegisterOptions{AllowMutate: false}); rerr != nil {
			return claudeMCPToggledMsg{Err: rerr}
		}
		return claudeMCPToggledMsg{Summary: tr("ccmux-mcp registered with Claude Code (read-only)")}
	}
}
