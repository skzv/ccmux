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

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// newSessionFormModel is the modal the Sessions tab opens on `n`.
// Smaller than the new-project form — bare sessions don't carry a
// description, an agent, or any scaffold context — just three rows:
//
//	name      tmux session name (default: auto-generated)
//	device    which device to spawn on (picker: local + reachable peers)
//	workdir   working directory the shell opens in (default from config)
//
// Tab cycles between fields; ←/→ cycles the device picker; Enter
// submits, Esc cancels. The shape deliberately mirrors the
// newProjectFormModel so a user who learned one form recognizes
// the other.
type newSessionFormModel struct {
	st          styles.Styles
	name        textinput.Model
	workdir     textinput.Model
	focus       int // 0 name, 1 workdir, 2 device
	err         string
	hosts       []hostChoice
	hostIdx     int
	defaultPath string // resolved sessions.default_dir for hint display
}

func newNewSessionForm(st styles.Styles, hosts []hostStatus, defaultDir string) newSessionFormModel {
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

	return newSessionFormModel{
		st:          st,
		name:        n,
		workdir:     w,
		focus:       0,
		hosts:       hostChoicesFrom(hosts),
		hostIdx:     0,
		defaultPath: defaultDir,
	}
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

// nsFocusCount is the row count for the form's focus cycling.
const nsFocusCount = 3

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
			if m.focus == 2 && len(m.hosts) > 0 {
				m.hostIdx = (m.hostIdx - 1 + len(m.hosts)) % len(m.hosts)
				return m, nil
			}
		case "right":
			if m.focus == 2 && len(m.hosts) > 0 {
				m.hostIdx = (m.hostIdx + 1) % len(m.hosts)
				return m, nil
			}
		case "enter":
			h := m.currentHost()
			submit := newBareSessionSubmitMsg{
				Name: strings.TrimSpace(m.name.Value()),
				Path: strings.TrimSpace(m.workdir.Value()),
				Host: h.Label,
			}
			if !h.Local {
				submit.Address = h.Address
				submit.DialHost = h.DialHost
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

func (m newSessionFormModel) View(width int) string {
	st := m.st
	title := st.Emphasis.Render("New shell session")
	hint := st.Subtitle.Render("Spawn a bare tmux session — no project scaffold, just a shell on the picked device.")

	nameLabel := st.Muted.Render("name        ")
	workLabel := st.Muted.Render("working dir ")
	hostLabel := st.Muted.Render("device      ")

	nameField := m.name.View()
	workField := m.workdir.View()
	hostField := m.renderHostPicker()
	rows := []*string{&nameField, &workField, &hostField}
	for i, r := range rows {
		if i == m.focus {
			*r = st.Emphasis.Render("▌ ") + *r
		} else {
			*r = "  " + *r
		}
	}

	keys := st.Muted.Render("tab: next field   ←/→: pick device   enter: create   esc: cancel")
	parts := []string{
		title,
		hint,
		"",
		nameLabel + nameField,
		workLabel + workField,
		hostLabel + hostField,
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
				Name: submit.Name,
				Path: submit.Path,
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
				dial = submit.Host
			}
			return remoteSessionStartedMsg{
				SessionName: res.Session,
				DialHost:    dial,
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
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tmux.New(ctx, name, path, shell); err != nil {
			return toastMsg{
				Text:  "tmux new-session: " + err.Error(),
				Kind:  toastError,
				Until: time.Now().Add(5 * time.Second),
			}
		}
		return bareSessionReadyMsg{Session: name}
	}
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
