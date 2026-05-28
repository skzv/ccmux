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
	"os/user"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/remoteattach"
	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tui/components"
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

// HelpBarProps returns the screen-specific key hints for the
// Network screen.
func (m networkModel) HelpBarProps(width int) components.HelpBarProps {
	return components.HelpBarProps{
		Hints: []components.KeyHint{
			{Key: "?", Label: "help", Priority: 10},
			{Key: "q", Label: "quit", Priority: 10},
			{Key: "enter", Label: "ssh", Priority: 7},
			{Key: "r", Label: "refresh", Priority: 4},
			{Key: "1-7", Label: "screens", Priority: 2},
		},
		Width: width,
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
//
// Failure routing: we emit attachExitedMsg with RemoteSSHTarget
// populated rather than the old refreshAfterDetachMsg. Two
// reasons: (1) the App's attachExitedMsg handler surfaces the err
// as a toast instead of silently swallowing it, and (2) when the
// err looks like an auth failure the same handler routes to the
// SSH setup wizard automatically — which is exactly what a user
// hitting Enter on a not-yet-keyed peer would want.
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
	cmd := remoteattach.SSHInteractive(dial)
	if dbg := debugLogger(); dbg != nil {
		dbg.Printf("network ssh: %s", dial)
	}
	rt := remoteTargetForSSH(*sel, dial)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		// err is nil on a clean detach (user typed `exit` on the
		// remote shell). Pass it through unchanged — the
		// attachExitedMsg handler treats nil-Err as success.
		return attachExitedMsg{Err: err, RemoteSSHTarget: rt}
	})
}

// remoteTargetForSSH derives the user/host/port the wizard would
// need if the SSH attempt fails with auth-denied. The dial string
// may already carry a `user@` prefix (when the row's User field
// is set); we parse that back out so the wizard can route to the
// right account, not just the local $USER.
//
// For auto-discovered peers (no configured User), we fall back to
// the local $USER — matching what `ssh dial` would have done by
// default. This is the right guess on personal-dev machines where
// the local + remote usernames match; if they don't, the wizard's
// password step will fail and the user can re-run via
// `ccmux host setup-ssh otheruser@host` with the right account.
func remoteTargetForSSH(sel hostStatus, dial string) *attachRemoteTarget {
	port := sel.SSHPort
	if port == 0 {
		port = 22
	}
	// OpenShellOnSetup: the Network-tab Enter flow is "get me into
	// this device", so a post-failure SSH setup should finish by
	// opening the shell, not stranding the user on the list.
	rt := &attachRemoteTarget{Host: dial, Port: port, TailscaleSSH: sel.TailscaleSSH, OpenShellOnSetup: true}
	if i := strings.Index(dial, "@"); i >= 0 {
		rt.User = dial[:i]
		rt.Host = dial[i+1:]
	}
	if rt.User == "" && sel.User != "" {
		rt.User = sel.User
	}
	if rt.User == "" {
		if u, err := user.Current(); err == nil {
			rt.User = u.Username
		}
	}
	return rt
}

// SetupSSHCmd builds the tea.Cmd that opens the SSH setup wizard
// for the selected peer. Returns nil for unactionable rows (local,
// mobile). For dispatch on the 's' key from the Network screen.
// The wizard handles the rest — probe, password prompt, install,
// validate, optionally enumerate other users on the same host.
func (m networkModel) SetupSSHCmd() tea.Cmd {
	sel := m.Selected()
	if sel == nil || sel.Local || sel.Mobile {
		return nil
	}
	host := sel.DialHost
	if host == "" {
		host = sel.Address
	}
	if host == "" {
		return nil
	}
	if sel.TailscaleSSH {
		// Tailscale already handles auth via the tailnet identity —
		// no key install required. Tell the user instead of opening
		// a wizard that would then short-circuit on the probe.
		return func() tea.Msg {
			return toastMsg{
				Text:  host + " uses Tailscale SSH — no setup needed, just attach",
				Kind:  toastInfo,
				Until: time.Now().Add(5 * time.Second),
			}
		}
	}
	port := sel.SSHPort
	if port == 0 {
		port = 22
	}
	target := sshsetup.Target{
		User: sel.User,
		Host: host,
		Port: port,
	}
	if target.User == "" {
		if u, err := user.Current(); err == nil {
			target.User = u.Username
		}
	}
	return func() tea.Msg {
		// OpenShell: the user picked a device on the Network tab to
		// get INTO it. Once setup succeeds, drop them into the shell
		// rather than stranding them back on the list.
		return openSSHWizardMsg{target: target, resume: sshWizardResume{OpenShell: true}}
	}
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
				// summarizePath collapses the user's $HOME to `~/`
				// so a sandbox /tmp/... path doesn't leak into
				// public-demo GIFs. The local-row Address is a
				// unix:// socket URL — strip the scheme before
				// matching against HOME (filepath.Clean doesn't
				// know about URL schemes), then put it back.
				display := sel.Address
				if strings.HasPrefix(display, "unix://") {
					display = "unix://" + summarizePath(strings.TrimPrefix(display, "unix://"))
				} else {
					display = summarizePath(display)
				}
				rows = append(rows, "  address   "+display)
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
	// Append a small "ts-ssh" badge when the peer has Tailscale
	// SSH enabled — signals to the user that no key-install
	// wizard is needed and that auth flows through their tailnet
	// identity (governed by ACLs in the admin console).
	if h.TailscaleSSH && !h.Local && !h.Mobile {
		badge := st.Key.Render("ts-ssh")
		if tag == "" {
			tag = badge
		} else {
			tag = badge + "  " + tag
		}
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
