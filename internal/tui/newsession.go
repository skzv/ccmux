package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// newSessionFormModel is the modal the Sessions tab opens on `n`.
// Four rows:
//
//	name      tmux session name (default: auto-generated)
//	workdir   working directory the agent/shell opens in
//	device    which device to spawn on
//	agent     which AI agent to launch (claude / codex / antigravity)
//	          or "shell" for a plain $SHELL with no agent.
//
// The agent picker is the fix for the multi-agent regression where
// Sessions-tab `n` always landed the user in a bare shell, even with
// agents installed — the multi-agent refactor abstracted everywhere
// else but left this form launching $SHELL. Default selection comes
// from sessions.default_agent in config.toml; users who want the old
// shell-only behaviour set that to "shell".
//
// Tab cycles fields; ←/→ cycles the device and agent pickers; Enter
// submits, Esc cancels. Shape mirrors newProjectFormModel so a user
// who learned one form recognizes the other.
type newSessionFormModel struct {
	st          styles.Styles
	name        textinput.Model
	workdir     textinput.Model
	focus       int // 0 name, 1 workdir, 2 device, 3 agent
	err         string
	hosts       []hostChoice
	hostIdx     int
	defaultPath string // resolved sessions.default_dir for hint display

	// agents is the picker model. A sentinel entry with ID == "" at
	// the end represents "shell — no agent"; selecting it preserves
	// the pre-picker behaviour. Constructor seeds from
	// agent.AllInstalled so the user only sees runnable choices.
	agents   []sessionAgentChoice
	agentIdx int
}

// sessionAgentChoice is one row in the agent picker. The sentinel
// (ID == "") represents the "shell, no agent" escape hatch.
type sessionAgentChoice struct {
	ID    agent.ID
	Label string
}

// agentChoicesForBareSession builds the picker rows: installed agents
// first (in agent.All() order), then a "shell" sentinel. If no agent
// binaries resolve we still seed every agent so the form is usable —
// the daemon-side spawn will surface a missing-binary error if the
// user picks one that isn't on PATH (same behaviour as the new-project
// picker).
func agentChoicesForBareSession() []sessionAgentChoice {
	installed := agent.AllInstalled(context.Background())
	if len(installed) == 0 {
		installed = agent.All()
	}
	out := make([]sessionAgentChoice, 0, len(installed)+1)
	for _, a := range installed {
		out = append(out, sessionAgentChoice{ID: a.ID(), Label: a.DisplayName()})
	}
	out = append(out, sessionAgentChoice{ID: "", Label: "shell (no agent)"})
	return out
}

func newNewSessionForm(st styles.Styles, hosts []hostStatus, defaultDir, defaultAgent string) newSessionFormModel {
	n := textinput.New()
	n.Placeholder = "auto (c-shell-<runid>)"
	n.CharLimit = 64
	n.Width = 40
	n.Prompt = ""
	n.Focus()

	w := textinput.New()
	// Don't pre-fill — leave it empty so submit defaults to the
	// daemon-side resolved path. Placeholder communicates the
	// fallback.
	w.Placeholder = defaultDirPlaceholder(defaultDir)
	w.CharLimit = 256
	w.Width = 60
	w.Prompt = ""

	agents := agentChoicesForBareSession()
	return newSessionFormModel{
		st:          st,
		name:        n,
		workdir:     w,
		focus:       0,
		hosts:       hostChoicesFrom(hosts),
		hostIdx:     0,
		defaultPath: defaultDir,
		agents:      agents,
		agentIdx:    indexOfDefaultAgent(agents, defaultAgent),
	}
}

// indexOfDefaultAgent picks the picker's initial cursor row from the
// user's configured default. Matches by canonical agent ID (via
// agent.ParseID so the "gemini" alias works for back-compat), and
// accepts the literal "shell" to mean "no agent". Empty / unrecognized
// values fall back to row 0 (the first installed agent) — the spirit
// of the multi-agent refactor.
func indexOfDefaultAgent(agents []sessionAgentChoice, configDefault string) int {
	def := strings.ToLower(strings.TrimSpace(configDefault))
	if def == "shell" {
		for i, a := range agents {
			if a.ID == "" {
				return i
			}
		}
		return 0
	}
	if def == "" {
		return 0
	}
	if id, ok := agent.ParseID(def); ok {
		for i, a := range agents {
			if a.ID == id {
				return i
			}
		}
	}
	return 0
}

// defaultDirPlaceholder is the muted placeholder text for the
// workdir field. Centralized so the empty-default and explicit-
// default cases render symmetrically.
func defaultDirPlaceholder(defaultDir string) string {
	if strings.TrimSpace(defaultDir) == "" {
		return "~ (daemon's $HOME if blank)"
	}
	return defaultDir + " (from sessions.default_dir; edit to override)"
}

// nsFocusCount is the row count for the form's focus cycling:
// 0 name, 1 workdir, 2 device, 3 agent.
const nsFocusCount = 4

func (m newSessionFormModel) Update(msg tea.Msg) (newSessionFormModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			return m, func() tea.Msg { return newBareSessionCancelMsg{} }
		case "tab", "down":
			m.focus = (m.focus + 1) % nsFocusCount
			m.applyFocus()
			return m, textinput.Blink
		case "shift+tab", "up":
			m.focus = (m.focus + nsFocusCount - 1) % nsFocusCount
			m.applyFocus()
			return m, textinput.Blink
		case "left":
			switch {
			case m.focus == 2 && len(m.hosts) > 0:
				m.hostIdx = (m.hostIdx - 1 + len(m.hosts)) % len(m.hosts)
				return m, nil
			case m.focus == 3 && len(m.agents) > 0:
				m.agentIdx = (m.agentIdx - 1 + len(m.agents)) % len(m.agents)
				return m, nil
			}
		case "right":
			switch {
			case m.focus == 2 && len(m.hosts) > 0:
				m.hostIdx = (m.hostIdx + 1) % len(m.hosts)
				return m, nil
			case m.focus == 3 && len(m.agents) > 0:
				m.agentIdx = (m.agentIdx + 1) % len(m.agents)
				return m, nil
			}
		case "enter":
			h := m.currentHost()
			a := m.currentAgent()
			submit := newBareSessionSubmitMsg{
				Name:  strings.TrimSpace(m.name.Value()),
				Path:  strings.TrimSpace(m.workdir.Value()),
				Host:  h.Label,
				Agent: a.ID,
			}
			if !h.Local {
				submit.Address = h.Address
				submit.DialHost = h.DialHost
				submit.User = h.User
				submit.Mosh = h.Mosh
			}
			return m, func() tea.Msg { return submit }
		}
	}
	var cmd tea.Cmd
	switch m.focus {
	case 0:
		m.name, cmd = m.name.Update(msg)
	case 1:
		m.workdir, cmd = m.workdir.Update(msg)
	}
	return m, cmd
}

func (m *newSessionFormModel) applyFocus() {
	switch m.focus {
	case 0:
		m.name.Focus()
		m.workdir.Blur()
	case 1:
		m.name.Blur()
		m.workdir.Focus()
	default:
		m.name.Blur()
		m.workdir.Blur()
	}
}

func (m newSessionFormModel) currentHost() hostChoice {
	if len(m.hosts) == 0 {
		return hostChoice{Label: "local", Local: true}
	}
	return m.hosts[m.hostIdx]
}

// currentAgent returns the picker's current selection. Defensive
// fallback to the "shell" sentinel when the agents slice is empty —
// constructor seeds it, so this is unreachable in practice.
func (m newSessionFormModel) currentAgent() sessionAgentChoice {
	if len(m.agents) == 0 {
		return sessionAgentChoice{ID: "", Label: "shell (no agent)"}
	}
	return m.agents[m.agentIdx]
}

func (m newSessionFormModel) View(width int) string {
	st := m.st
	title := st.Emphasis.Render("New session")
	hint := st.Subtitle.Render("Spawn a tmux session running the picked agent (or a bare shell) on the picked device.")

	nameLabel := st.Muted.Render("name        ")
	workLabel := st.Muted.Render("working dir ")
	hostLabel := st.Muted.Render("device      ")
	agentLabel := st.Muted.Render("agent       ")

	nameField := m.name.View()
	workField := m.workdir.View()
	hostField := m.renderHostPicker()
	agentField := m.renderAgentPicker()
	rows := []*string{&nameField, &workField, &hostField, &agentField}
	for i, r := range rows {
		if i == m.focus {
			*r = st.Emphasis.Render("▌ ") + *r
		} else {
			*r = "  " + *r
		}
	}

	keys := st.Muted.Render("tab: next field   ←/→: pick device/agent   enter: create   esc: cancel")
	parts := []string{
		title,
		hint,
		"",
		nameLabel + nameField,
		workLabel + workField,
		hostLabel + hostField,
		agentLabel + agentField,
		"",
		keys,
	}
	if m.err != "" {
		parts = append(parts, st.StatusError.Render("⚠ "+m.err))
	}
	return st.PaneFocused.Width(width - 2).Render(strings.Join(parts, "\n"))
}

// spawnBareSessionCmd dispatches the new-session submit to either
// the local daemon (via tmux.New + bareSessionReadyMsg) or the
// remote daemon (via daemon.NewBareSession → remoteSessionStartedMsg).
// Local creation runs inline rather than going through ccmuxd
// because the alternative — POST localhost socket → identical
// effect — adds round-trip latency for no benefit.
func spawnBareSessionCmd(submit newBareSessionSubmitMsg) tea.Cmd {
	return func() tea.Msg {
		// Remote case: hand off to the remote daemon.
		if submit.Host != "" && submit.Host != "local" && submit.Address != "" {
			cli := daemon.RemoteClient(submit.Address)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			res, err := cli.NewBareSession(ctx, daemon.NewBareSessionRequest{
				Name:  submit.Name,
				Path:  submit.Path,
				Agent: string(submit.Agent),
			})
			if err != nil {
				return toastMsg{
					Text:  "new session on " + submit.Host + ": " + err.Error(),
					Kind:  toastError,
					Until: time.Now().Add(6 * time.Second),
				}
			}
			dial := submit.DialHost
			if dial == "" {
				// Fall back to the display name — better than nothing,
				// though the user may need to configure DialHost.
				dial = submit.Host
			}
			return remoteSessionStartedMsg{
				SessionName: res.Session,
				DialHost:    dial,
				User:        submit.User,
				Mosh:        submit.Mosh,
			}
		}
		// Local case. Resolve workdir client-side using the same
		// rules the daemon would: explicit → $HOME (no config
		// fallback here because this code path doesn't read the
		// daemon's config; the form's placeholder already showed
		// the user what the config default is, and they typed
		// something or accepted the default).
		path := strings.TrimSpace(submit.Path)
		if path == "" {
			if home, err := os.UserHomeDir(); err == nil {
				path = home
			}
		}
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		} else if path == "~" {
			if home, err := os.UserHomeDir(); err == nil {
				path = home
			}
		}
		if _, err := os.Stat(path); err != nil {
			return toastMsg{
				Text:  "new session: path not found: " + path,
				Kind:  toastError,
				Until: time.Now().Add(5 * time.Second),
			}
		}
		name := submit.Name
		if name == "" {
			name = fmt.Sprintf("c-shell-%d", time.Now().UnixMilli())
		}
		launch := launchCmdForBareSession(submit.Agent)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tmux.New(ctx, name, path, launch); err != nil {
			return toastMsg{
				Text:  "tmux new-session: " + err.Error(),
				Kind:  toastError,
				Until: time.Now().Add(5 * time.Second),
			}
		}
		return bareSessionReadyMsg{Session: name}
	}
}

// launchCmdForBareSession picks the command tmux new-session will run.
// Returns the agent's LaunchCmd(false) when id resolves to a known
// agent; otherwise returns $SHELL (falling back to /bin/sh) so a
// missing $SHELL on the daemon's host doesn't produce a dead pane.
// Exposed for tests that pin the shape of the command for a given
// agent picker selection.
func launchCmdForBareSession(id agent.ID) string {
	if id != "" {
		if parsed, ok := agent.ParseID(string(id)); ok {
			return agent.ByID(parsed).LaunchCmd(false)
		}
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell
}

// renderHostPicker mirrors the helper on newProjectFormModel — same
// visual treatment (‹ Name › when focused, plain when not, count
// hint either way).
func (m newSessionFormModel) renderHostPicker() string {
	cur := m.currentHost().Label
	if len(m.hosts) <= 1 {
		return m.st.Muted.Render(cur + "  (only host available)")
	}
	hint := fmt.Sprintf("%d of %d", m.hostIdx+1, len(m.hosts))
	if m.focus == 2 {
		return "‹ " + m.st.Emphasis.Render(cur) + " ›   " + m.st.Muted.Render("("+hint+")")
	}
	return cur + "   " + m.st.Muted.Render("("+hint+")")
}

// renderAgentPicker mirrors renderHostPicker for the agent row.
func (m newSessionFormModel) renderAgentPicker() string {
	cur := m.currentAgent().Label
	if len(m.agents) <= 1 {
		return m.st.Muted.Render(cur + "  (only agent available)")
	}
	hint := fmt.Sprintf("%d of %d", m.agentIdx+1, len(m.agents))
	if m.focus == 3 {
		return "‹ " + m.st.Emphasis.Render(cur) + " ›   " + m.st.Muted.Render("("+hint+")")
	}
	return cur + "   " + m.st.Muted.Render("("+hint+")")
}
