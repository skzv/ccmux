package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// settingsModel surfaces ccmux's own config.toml with toggles and links,
// plus a Moshi/moshi-hook status block at the top so users can see at a
// glance whether the mobile push pipeline is set up.
type settingsModel struct {
	st          styles.Styles
	km          Keymap
	cfg         config.Config
	version     string
	moshiState  moshi.Status
	moshiCheck  time.Time
}

func newSettings(st styles.Styles, km Keymap, cfg config.Config, version string) settingsModel {
	// One-shot detect at construction so the first render of this screen
	// shows real status. Subsequent refreshes happen in Update() at a
	// 30-second cadence.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return settingsModel{
		st: st, km: km, cfg: cfg, version: version,
		moshiState: moshi.Detect(ctx),
		moshiCheck: time.Now(),
	}
}

func (m settingsModel) Update(msg tea.Msg) (settingsModel, tea.Cmd) {
	// Refresh the cached moshi state at most every 30s while this screen
	// is visible, so the user sees a current picture without us shelling
	// out on every keystroke.
	if time.Since(m.moshiCheck) > 30*time.Second {
		m.moshiState = moshi.Detect(context.Background())
		m.moshiCheck = time.Now()
	}
	return m, nil
}

func (m settingsModel) View(width, height int) string {
	cfgPath, _ := config.Path()
	lines := []string{
		m.st.Emphasis.Render("Settings"),
		"",
		m.renderMoshiBlock(),
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
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
}

// renderMoshiBlock shows the most useful one-glance view of the mobile
// push pipeline state. The exact wording follows the steps in
// `ccmux moshi-setup`, so users know what to do next.
func (m settingsModel) renderMoshiBlock() string {
	s := m.moshiState
	title := m.st.Subtitle.Render("Moshi (mobile push)")
	var lines []string
	switch {
	case !s.BinaryInstalled:
		lines = []string{
			m.st.Muted.Render("  · moshi-hook not installed."),
			"  Run " + m.st.Key.Render("ccmux moshi-setup") + " in a shell to install + pair.",
		}
	case !s.Paired:
		lines = []string{
			m.st.StatusWarning.Render("  · moshi-hook installed but not paired."),
			"  Run " + m.st.Key.Render("ccmux moshi-setup") + " and provide a token from the Moshi app.",
		}
	case !s.HooksInstalled:
		lines = []string{
			m.st.StatusWarning.Render("  ⚠ paired but Claude Code hooks not wired."),
			"  Run " + m.st.Key.Render("moshi-hook install"),
		}
	case !s.ServiceRunning:
		lines = []string{
			m.st.StatusWarning.Render("  ⚠ hooks wired but daemon not running."),
			"  Run " + m.st.Key.Render("brew services start moshi-hook"),
		}
	default:
		lines = []string{
			m.st.StatusGood.Render("  ✓ installed, paired, hooks wired, service running."),
			m.st.Muted.Render("  ccmuxd will defer to moshi-hook for push notifications."),
		}
	}
	return strings.Join(append([]string{title}, lines...), "\n")
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
