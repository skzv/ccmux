package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// settingsModel surfaces ccmux's own config.toml with toggles and links.
type settingsModel struct {
	st      styles.Styles
	km      Keymap
	cfg     config.Config
	version string
}

func newSettings(st styles.Styles, km Keymap, cfg config.Config, version string) settingsModel {
	return settingsModel{st: st, km: km, cfg: cfg, version: version}
}

func (m settingsModel) Update(msg tea.Msg) (settingsModel, tea.Cmd) {
	return m, nil
}

func (m settingsModel) View(width, height int) string {
	cfgPath, _ := config.Path()
	lines := []string{
		m.st.Emphasis.Render("Settings"),
		"",
		fmt.Sprintf("ccmux version    %s", m.version),
		fmt.Sprintf("config file      %s", cfgPath),
		"",
		m.st.Subtitle.Render("Projects"),
		fmt.Sprintf("  root           %s", m.cfg.Projects.Root),
		"",
		m.st.Subtitle.Render("Theme"),
		fmt.Sprintf("  active         %s", m.cfg.Theme),
		m.st.Muted.Render("  (theme picker coming in v0.2)"),
		"",
		m.st.Subtitle.Render("Sleep prevention"),
		fmt.Sprintf("  idle release   %d minutes", m.cfg.Sleep.IdleReleaseMinutes),
		fmt.Sprintf("  dangerous batt %v", m.cfg.Sleep.DangerousKeepAwakeOnBattery),
		fmt.Sprintf("  low-batt cutoff %d%%", m.cfg.Sleep.LowBatteryCutoff),
		"",
		m.st.Subtitle.Render("Daemon"),
		fmt.Sprintf("  poll interval  %ds", m.cfg.Daemon.PollIntervalSeconds),
		fmt.Sprintf("  needs-input idle %ds", m.cfg.Daemon.IdleSecondsForNeedsInput),
		fmt.Sprintf("  tailnet listen %v (port %d)", m.cfg.Daemon.ListenTailnet, m.cfg.Daemon.TailnetPort),
		"",
		m.st.Subtitle.Render("Remote hosts"),
		m.renderHosts(),
	}
	return m.st.Pane.Width(width - 2).Height(height).Render(strings.Join(lines, "\n"))
}

func (m settingsModel) renderHosts() string {
	if len(m.cfg.Hosts) == 0 {
		return m.st.Muted.Render("  (none — add with `ccmux host add <name> <address>`)")
	}
	out := []string{}
	for _, h := range m.cfg.Hosts {
		out = append(out, fmt.Sprintf("  %s  %s@%s  mosh=%v", m.st.HostColor(h.Name).Render("●"), h.User, h.Address, h.Mosh))
	}
	return strings.Join(out, "\n")
}
