// Package tui — Network screen.
//
// One row per device the dashboard knows about (local + configured
// remotes + auto-discovered tailnet peers + mobile peers). Enter
// drops into a plain ssh shell on the selected peer — no Claude
// session, no tmux attach, just a shell. Useful for ad-hoc admin
// tasks on the same machines ccmux is supervising.
//
// Mobile peers are listed but not actionable from here (use the
// Moshi app instead). Local is listed but Enter is a no-op since
// the user is already there.
package tui

import (
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

type networkModel struct {
	st     styles.Styles
	km     Keymap
	hosts  []hostStatus
	cursor int
}

func newNetwork(st styles.Styles, km Keymap) networkModel {
	return networkModel{st: st, km: km}
}

// SetHosts mirrors the Sessions/Dashboard pattern — the App pushes
// the latest hosts list in via this setter after each refresh tick.
func (m *networkModel) SetHosts(hs []hostStatus) {
	m.hosts = hs
	if m.cursor >= len(hs) {
		m.cursor = max0(len(hs) - 1)
	}
}

func (m networkModel) Update(msg tea.Msg) (networkModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case keyMatches(km, m.km.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case keyMatches(km, m.km.Down):
			if m.cursor < len(m.hosts)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

// Selected returns the row under the cursor or nil when the list is
// empty / cursor out of range.
func (m networkModel) Selected() *hostStatus {
	if m.cursor < 0 || m.cursor >= len(m.hosts) {
		return nil
	}
	h := m.hosts[m.cursor]
	return &h
}

// SSHCmd builds the tea.Cmd that opens an interactive shell on the
// selected peer. Returns nil when the selection isn't ssh-able
// (local, mobile, missing dial host). Caller is responsible for
// dispatching toasts on nil.
func (m networkModel) SSHCmd() tea.Cmd {
	sel := m.Selected()
	if sel == nil || sel.Local || sel.Mobile {
		return nil
	}
	dial := sel.DialHost
	if dial == "" {
		dial = dialAddrFor(*sel)
	}
	if dial == "" {
		return nil
	}
	cmd := exec.Command("ssh", "-t", dial)
	if dbg := debugLogger(); dbg != nil {
		dbg.Printf("network ssh: %s", dial)
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return refreshAfterDetachMsg{}
	})
}

func (m networkModel) View(width, height int) string {
	st := m.st
	narrow := isNarrow(width)

	// Header: the device count (T1) stays; the inline action hint
	// (T2) is dropped on narrow.
	header := st.Emphasis.Render("Network") + "  "
	if narrow {
		header += st.Muted.Render(fmt.Sprintf("(%d)", len(m.hosts)))
	} else {
		header += st.Muted.Render(fmt.Sprintf("(%d device(s) — enter: ssh   r: refresh)", len(m.hosts)))
	}

	if len(m.hosts) == 0 {
		parts := []string{header, "", st.Muted.Render("No devices discovered yet.")}
		// The explanatory help paragraph is T2 — dropped on narrow.
		if !narrow {
			parts = append(parts,
				"",
				"This screen lists every machine on your tailnet that ccmux can see.",
				"Make sure tailscale is signed in ("+st.Key.Render("tailscale status")+") and try "+st.Key.Render("r")+" to refresh.",
			)
		}
		return st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(strings.Join(parts, "\n"))
	}

	// The colour legend is T2 — dropped on narrow.
	rows := []string{header, ""}
	if !narrow {
		rows = append(rows, st.Muted.Render(networkLegend()), "")
	}
	for i, h := range m.hosts {
		row := m.renderRow(h, i == m.cursor)
		rows = append(rows, row)
	}

	rows = append(rows, "")
	if sel := m.Selected(); sel != nil {
		rows = append(rows, st.Subtitle.Render("Selected"))
		rows = append(rows, "  name      "+sel.Name)
		// The os / address / dial / ccmuxd-version detail is T2 —
		// dropped on narrow so the Selected block stays compact.
		if !narrow {
			if sel.OS != "" {
				rows = append(rows, "  os        "+sel.OS)
			}
			if sel.Address != "" {
				rows = append(rows, "  address   "+sel.Address)
			}
			if sel.DialHost != "" {
				rows = append(rows, "  dial      "+sel.DialHost)
			}
			if sel.Version != "" {
				rows = append(rows, "  ccmuxd    "+sel.Version)
			}
		}
		rows = append(rows, "")
		switch {
		case sel.Local:
			rows = append(rows, st.Muted.Render("This is the local machine — nothing to ssh into."))
		case sel.Mobile:
			rows = append(rows, st.Muted.Render("Mobile device — connect via the Moshi iOS app, not ssh."))
		default:
			rows = append(rows, st.Key.Render("enter")+"  ssh -t "+sel.DialHostOrAddr())
		}
	}

	return st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(strings.Join(rows, "\n"))
}

func (m networkModel) renderRow(h hostStatus, selected bool) string {
	st := m.st
	icon := iconForHost(h, st)
	name := h.Name
	if h.Local {
		name += "  " + st.Muted.Render("(this device)")
	}
	tag := ""
	switch {
	case h.Mobile:
		tag = st.Muted.Render("mobile — use Moshi")
	case h.NeedsInstall:
		tag = st.Muted.Render("ccmuxd unreachable")
	case h.OS != "" && h.Version != "":
		tag = st.Muted.Render(h.OS + " · " + h.Version)
	case h.Version != "":
		tag = st.Muted.Render(h.Version)
	}
	row := fmt.Sprintf("  %s %s    %s", icon, name, tag)
	if selected {
		row = st.ListItemSelected.Render(row)
	}
	return row
}

func networkLegend() string {
	return "● online (ccmuxd reachable)   ○ ccmuxd unreachable   📱 mobile (use Moshi)"
}

// DialHostOrAddr is a small accessor that returns whichever of
// DialHost / Address is non-empty. Renders in the "Selected" detail
// pane to show the user exactly what ssh will be invoked with.
func (h hostStatus) DialHostOrAddr() string {
	if h.DialHost != "" {
		return h.DialHost
	}
	return dialAddrFor(h)
}
