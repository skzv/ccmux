package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// addHostFormModel is the modal opened by pressing `a` on the Network
// screen — the TUI mirror of `ccmux host add`. Five fields: name,
// address, user (optional), ssh port (default 22), and mosh toggle.
type addHostFormModel struct {
	st      styles.Styles
	name    textinput.Model
	address textinput.Model
	user    textinput.Model
	sshPort textinput.Model
	mosh    bool
	focus   int // 0=name, 1=address, 2=user, 3=sshPort, 4=mosh
	err     string
}

const (
	addHostFieldCount = 5
	addHostInputWidth = 44
)

type (
	addHostSubmitMsg struct{ Host config.Host }
	addHostCancelMsg struct{}
	hostAddedMsg     struct {
		Name string
		Err  error
	}
	hostRemovedMsg struct {
		Name string
		Err  error
	}
)

func newAddHostForm(st styles.Styles) addHostFormModel {
	mk := func(placeholder string, limit int) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.CharLimit = limit
		ti.Width = addHostInputWidth
		ti.Prompt = ""
		return ti
	}
	name := mk("mac-mini", 64)
	name.Focus()
	addr := mk("100.64.0.5 or mini.tail.ts.net", 128)
	user := mk(tr("optional (default: local user)"), 64)
	port := mk("22", 5)

	return addHostFormModel{
		st:      st,
		name:    name,
		address: addr,
		user:    user,
		sshPort: port,
		mosh:    true,
		focus:   0,
	}
}

func (m *addHostFormModel) applyFocus() {
	m.name.Blur()
	m.address.Blur()
	m.user.Blur()
	m.sshPort.Blur()
	switch m.focus {
	case 0:
		m.name.Focus()
	case 1:
		m.address.Focus()
	case 2:
		m.user.Focus()
	case 3:
		m.sshPort.Focus()
	}
}

func (m addHostFormModel) Update(msg tea.Msg) (addHostFormModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			return m, func() tea.Msg { return addHostCancelMsg{} }
		case "tab", "down":
			m.focus = (m.focus + 1) % addHostFieldCount
			m.applyFocus()
			return m, textinput.Blink
		case "shift+tab", "up":
			m.focus = (m.focus + addHostFieldCount - 1) % addHostFieldCount
			m.applyFocus()
			return m, textinput.Blink
		case "left", "right", " ":
			if m.focus == 4 {
				m.mosh = !m.mosh
				return m, nil
			}
		case "enter":
			h, err := m.validate()
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			return m, func() tea.Msg { return addHostSubmitMsg{Host: h} }
		}
	}
	var cmd tea.Cmd
	switch m.focus {
	case 0:
		m.name, cmd = m.name.Update(msg)
	case 1:
		m.address, cmd = m.address.Update(msg)
	case 2:
		m.user, cmd = m.user.Update(msg)
	case 3:
		m.sshPort, cmd = m.sshPort.Update(msg)
	}
	return m, cmd
}

func (m addHostFormModel) validate() (config.Host, error) {
	name := strings.TrimSpace(m.name.Value())
	if name == "" {
		return config.Host{}, fmt.Errorf("%s", tr("host name is required"))
	}
	if strings.ContainsAny(name, " \t\r\n") || strings.HasPrefix(name, "-") {
		return config.Host{}, fmt.Errorf("%s", tr("host name cannot start with '-' or contain whitespace"))
	}
	addr := strings.TrimSpace(m.address.Value())
	if addr == "" {
		return config.Host{}, fmt.Errorf("%s", tr("address is required"))
	}
	if strings.ContainsAny(addr, " \t\r\n") || strings.HasPrefix(addr, "-") {
		return config.Host{}, fmt.Errorf("%s", tr("address cannot start with '-' or contain whitespace"))
	}
	user := strings.TrimSpace(m.user.Value())
	if user != "" && (strings.ContainsAny(user, " \t\r\n@") || strings.HasPrefix(user, "-")) {
		return config.Host{}, fmt.Errorf("%s", tr("invalid SSH user"))
	}
	sshPort := 22
	if raw := strings.TrimSpace(m.sshPort.Value()); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 65535 {
			return config.Host{}, fmt.Errorf("%s", tr("ssh port must be 1..65535"))
		}
		sshPort = p
	}
	return config.Host{
		Name:    name,
		Address: addr,
		User:    user,
		Port:    7474,
		SSHPort: sshPort,
		Mosh:    m.mosh,
	}, nil
}

func (m *addHostFormModel) FitTo(width int) {
	textW := maxInt(16, width-4)
	labelW := labelColumn(10, textW/2, tr("name"), tr("address"), tr("user"), tr("ssh port"), tr("mosh"))
	fieldW := maxInt(8, minInt(addHostInputWidth, textW-labelW-3))
	fitInput(&m.name, fieldW)
	fitInput(&m.address, fieldW)
	fitInput(&m.user, fieldW)
	fitInput(&m.sshPort, fieldW)
}

func (m addHostFormModel) View(width int) string {
	st := m.st
	modalW := maxInt(28, minInt(68, width-4))
	m.FitTo(modalW)
	textW := maxInt(16, modalW-4)
	labels := []string{tr("name"), tr("address"), tr("user"), tr("ssh port"), tr("mosh")}
	labelW := labelColumn(10, textW/2, labels...)
	fieldW := maxInt(8, textW-labelW-2)

	moshVal := tr("off")
	if m.mosh {
		moshVal = tr("on")
	}
	if m.focus == 4 {
		moshVal = st.Emphasis.Render("[" + moshVal + "]")
	} else {
		moshVal = st.Muted.Render("[" + moshVal + "]")
	}

	row := func(i int, val string) string {
		lbl := columnLabel(labels[i], labelW)
		if m.focus == i {
			lbl = st.Emphasis.Render(lbl)
		} else {
			lbl = st.Muted.Render(lbl)
		}
		return lbl + "  " + truncate(val, fieldW)
	}

	lines := []string{
		st.Emphasis.Render(tr("Add remote host")),
		st.Subtitle.Render(tr("Saves a [[host]] entry to config.toml (mirrors `ccmux host add`).")),
		"",
		row(0, m.name.View()),
		row(1, m.address.View()),
		row(2, m.user.View()),
		row(3, m.sshPort.View()),
		row(4, moshVal),
	}
	if m.err != "" {
		lines = append(lines, "", st.StatusError.Render("⚠ "+m.err))
	}
	lines = append(lines, "", st.Muted.Render(tr("tab: next field   ←/→: toggle mosh   enter: save   esc: cancel")))
	return st.PaneFocused.Width(modalW).Render(strings.Join(lines, "\n"))
}
