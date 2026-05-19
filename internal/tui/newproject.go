package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// newProjectFormModel is the modal form rendered over the Projects screen
// when the user presses `n` to create a new project. Four fields:
// Name (required), Description (optional but recommended — the agent
// sees it as its first prompt), Host (where to scaffold — local or
// any reachable peer running ccmuxd), and Agent (which AI to launch
// in the new session — claude / codex / antigravity).
//
// Tab cycles Name → Description → Host → Agent. On the Host and
// Agent rows, ←/→ cycle their selections. The submitted message
// carries enough addressing info (Host display name, Address for
// ccmuxd POST, DialHost for ssh-attach) plus the chosen Agent that
// the dispatcher can route without re-resolving.
type newProjectFormModel struct {
	st    styles.Styles
	name  textinput.Model
	desc  textinput.Model
	focus int // 0 = name, 1 = desc, 2 = host, 3 = agent
	err   string

	// hosts is the device picker model. Always at least one entry
	// (local). Built once at form-open time from the App's current
	// reachable-hosts list so the picker reflects what was on-screen
	// when the user pressed `n`.
	hosts   []hostChoice
	hostIdx int

	// agents is the agent picker model. Always at least one entry
	// (claude) so the form is always submittable, even on a machine
	// with no agent binaries installed (the daemon-side scaffold
	// will surface the install error). Order follows agent.All()'s
	// canonical claude→codex→antigravity sequence; the default cursor
	// position is index 0 (claude) for back-compat continuity.
	agents   []agent.Agent
	agentIdx int
}

// hostChoice is one row in the device picker. Local is true exactly
// once (the local device). Address/DialHost/User/Mosh are only
// populated for remote choices.
type hostChoice struct {
	Label    string // shown to the user
	Local    bool
	Address  string // ccmuxd http "host:port" for remote daemon
	DialHost string // bare hostname/IP for ssh/mosh attach after scaffold
	User     string // login user; empty → client's own username
	Mosh     bool   // prefer mosh over ssh for this host
}

// newNewProjectForm builds the form. `hosts` is the live slice from
// the App (reachable peers). If empty, the picker still shows "local"
// so the form is always submittable. The agent picker is populated
// with everything that's installed; if none is detected we fall back
// to agent.All() so the form is still usable (the submit will surface
// the missing-binary error from the daemon side).
func newNewProjectForm(st styles.Styles, hosts []hostStatus) newProjectFormModel {
	n := textinput.New()
	n.Placeholder = "my-project"
	n.CharLimit = 64
	n.Width = 40
	n.Prompt = ""
	n.Focus()

	d := textinput.New()
	d.Placeholder = "what are you building? (one sentence; the agent sees this as your first message)"
	d.CharLimit = 240
	d.Width = 70
	d.Prompt = ""

	agents := agent.AllInstalled(context.Background())
	if len(agents) == 0 {
		agents = agent.All()
	}

	return newProjectFormModel{
		st:       st,
		name:     n,
		desc:     d,
		focus:    0,
		hosts:    hostChoicesFrom(hosts),
		hostIdx:  0,
		agents:   agents,
		agentIdx: 0,
	}
}

// hostChoicesFrom flattens the App's hostStatus list into picker rows.
// Order: local first, then each reachable peer running ccmuxd, sorted
// by display name for stability. Mobile/NeedsInstall rows are dropped —
// scaffolding requires a working ccmuxd on the target.
func hostChoicesFrom(hosts []hostStatus) []hostChoice {
	out := []hostChoice{{Label: "local", Local: true}}
	for _, h := range hosts {
		if h.Local {
			continue
		}
		if h.NeedsInstall || h.Mobile {
			continue
		}
		if !h.OK {
			continue
		}
		out = append(out, hostChoice{
			Label:    shortHostname(h.Name),
			Address:  h.Address,
			DialHost: h.DialHost,
			User:     h.User,
			Mosh:     h.Mosh,
		})
	}
	return out
}

// focusCount enumerates rows: name (0), desc (1), host (2), agent (3).
// Cycling math uses this so adding a future row only touches one spot.
const focusCount = 4

func (m newProjectFormModel) Update(msg tea.Msg) (newProjectFormModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			return m, func() tea.Msg { return newProjectCancelMsg{} }
		case "tab", "down":
			m.focus = (m.focus + 1) % focusCount
			m.applyFocus()
			return m, textinput.Blink
		case "shift+tab", "up":
			m.focus = (m.focus + focusCount - 1) % focusCount
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
			name := strings.TrimSpace(m.name.Value())
			if name == "" {
				m.err = "name is required"
				return m, nil
			}
			h := m.currentHost()
			a := m.currentAgent()
			return m, func() tea.Msg {
				out := newProjectSubmitMsg{
					Name:        name,
					Description: strings.TrimSpace(m.desc.Value()),
					Host:        h.Label,
					Agent:       a.ID(),
				}
				if !h.Local {
					out.Address = h.Address
					out.DialHost = h.DialHost
				}
				return out
			}
		}
	}
	// Only the focused text input consumes the message — the picker
	// rows (host, agent) don't type-into anything, so we skip the
	// textinput Update when focus is on either to keep them pristine.
	var cmd tea.Cmd
	switch m.focus {
	case 0:
		m.name, cmd = m.name.Update(msg)
	case 1:
		m.desc, cmd = m.desc.Update(msg)
	}
	return m, cmd
}

// applyFocus syncs Focus()/Blur() across the two text inputs based on
// the current focus index. Picker rows (host, agent) have no input so
// both blur when focus lands on them.
func (m *newProjectFormModel) applyFocus() {
	if m.focus == 0 {
		m.name.Focus()
		m.desc.Blur()
	} else if m.focus == 1 {
		m.name.Blur()
		m.desc.Focus()
	} else {
		m.name.Blur()
		m.desc.Blur()
	}
}

// currentHost returns the picker's current selection. Returns the
// "local" entry if the picker is somehow empty (defensive — the
// constructor always seeds it with local).
func (m newProjectFormModel) currentHost() hostChoice {
	if len(m.hosts) == 0 {
		return hostChoice{Label: "local", Local: true}
	}
	return m.hosts[m.hostIdx]
}

// currentAgent returns the picker's current selection. Defensive
// fallback to agent.Default() (claude) if the agents slice is somehow
// empty — the constructor seeds it from AllInstalled() (or All() if
// nothing's installed) so this is unreachable in practice.
func (m newProjectFormModel) currentAgent() agent.Agent {
	if len(m.agents) == 0 {
		return agent.Default()
	}
	return m.agents[m.agentIdx]
}

// View returns a rendered modal sized to the available width. The caller
// places it inside an outer Pane; we don't draw our own border.
func (m newProjectFormModel) View(width int) string {
	st := m.st
	title := st.Emphasis.Render("New project")
	hint := st.Subtitle.Render("ccmux scaffolds the dirs, starts your AI agent, and sends your description as the first prompt.")

	nameLabel := st.Muted.Render("name        ")
	descLabel := st.Muted.Render("description ")
	hostLabel := st.Muted.Render("device      ")
	agentLabel := st.Muted.Render("agent       ")
	nameField := m.name.View()
	descField := m.desc.View()
	hostField := m.renderHostPicker()
	agentField := m.renderAgentPicker()

	// Four-state focus marker. Each row gets either the ▌ cursor
	// (when focused) or two spaces of padding so the columns stay
	// aligned. Switch is cheap and obviously correct; a "for-loop
	// over labels" version would be denser but harder to read.
	rows := []*string{&nameField, &descField, &hostField, &agentField}
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
		descLabel + descField,
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

// renderHostPicker shows the current selection plus left/right arrows
// when there's more than one choice. When the picker has focus we wrap
// it in "‹ x ›"; otherwise we just show the label so the eye doesn't
// see floating arrows on an inactive row.
func (m newProjectFormModel) renderHostPicker() string {
	cur := m.currentHost().Label
	if len(m.hosts) <= 1 {
		return m.st.Muted.Render(cur + "  (only host available)")
	}
	if m.focus == 2 {
		return "‹ " + m.st.Emphasis.Render(cur) + " ›   " +
			m.st.Muted.Render("("+m.hostCountHint()+")")
	}
	return cur + "   " + m.st.Muted.Render("("+m.hostCountHint()+")")
}

func (m newProjectFormModel) hostCountHint() string {
	if len(m.hosts) <= 1 {
		return "1 device"
	}
	return intToWord(m.hostIdx+1) + " of " + intToWord(len(m.hosts))
}

// renderAgentPicker mirrors renderHostPicker for the agent row. The
// rendered string includes the agent's display name (Claude Code,
// Codex, Antigravity CLI) plus a count hint, with the ‹›-arrow framing
// only when the row is focused — same UX as the device picker so
// users don't have to learn two patterns.
func (m newProjectFormModel) renderAgentPicker() string {
	cur := m.currentAgent().DisplayName()
	if len(m.agents) <= 1 {
		return m.st.Muted.Render(cur + "  (only agent available)")
	}
	if m.focus == 3 {
		return "‹ " + m.st.Emphasis.Render(cur) + " ›   " +
			m.st.Muted.Render("("+m.agentCountHint()+")")
	}
	return cur + "   " + m.st.Muted.Render("("+m.agentCountHint()+")")
}

func (m newProjectFormModel) agentCountHint() string {
	if len(m.agents) <= 1 {
		return "1 agent"
	}
	return intToWord(m.agentIdx+1) + " of " + intToWord(len(m.agents))
}

// intToWord renders small ints inline. Falls back to decimal above 10
// because nobody has eleven Mac minis.
func intToWord(n int) string {
	switch n {
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	case 4:
		return "4"
	case 5:
		return "5"
	case 6:
		return "6"
	case 7:
		return "7"
	case 8:
		return "8"
	case 9:
		return "9"
	case 10:
		return "10"
	}
	// Tiny inline itoa to avoid pulling strconv just for this.
	if n < 0 {
		return "?"
	}
	out := []byte{}
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
