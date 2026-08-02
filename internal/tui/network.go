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
//
// Source-grouped layout: rows are partitioned into four sections —
// Local, Configured, Discovered, Mobile — by `hostStatus.Source`.
// Each row carries a small vocabulary of status chips
// (`[Tailscale SSH ✓]`, `[↑ update]`, `[unreachable]`, `[SSH ✓]`,
// `[Moshi]`, `[no ccmuxd]`) so the user can read state without a
// separate legend.
package tui

import (
	"context"
	"fmt"
	"os/user"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/remoteattach"
	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tui/components"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// pairTelegramCmd mints a one-time Telegram pairing code via the local
// daemon and surfaces it as a toast. When the bridge is off, it points
// the user at the CLI registration instead.
func pairTelegramCmd(enabled bool) tea.Cmd {
	return func() tea.Msg {
		if !enabled {
			return toastMsg{Text: "Telegram bridge is off — run `ccmux telegram register`", Kind: toastError, Until: time.Now().Add(6 * time.Second)}
		}
		cli, err := daemon.LocalClient()
		if err != nil {
			return toastMsg{Text: "telegram: " + err.Error(), Kind: toastError, Until: time.Now().Add(6 * time.Second)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		resp, err := cli.TelegramPairCode(ctx)
		if err != nil {
			return toastMsg{Text: "telegram pair failed (is the daemon running with the bridge enabled?)", Kind: toastError, Until: time.Now().Add(8 * time.Second)}
		}
		text := "Send  /start " + resp.Code + "  to your bot"
		if resp.BotUsername != "" {
			text += " (@" + resp.BotUsername + ")"
		}
		return toastMsg{Text: text, Kind: toastInfo, Until: time.Now().Add(30 * time.Second)}
	}
}

type networkModel struct {
	st     styles.Styles
	km     Keymap
	hosts  []hostStatus
	cursor int

	// version is the local ccmux build version, used to compute the
	// `[↑ update]` chip by comparing against each peer's reported
	// ccmuxd Version. Pushed in by App at startup; empty disables the
	// chip entirely (no false positives on dev builds).
	version string

	// refreshing is true between the moment a refresh fires (either
	// `r` or screen-entry) and the moment SetHosts delivers the next
	// snapshot. While true the chip column renders the spinner for
	// every row instead of the resolved chips — visual cue that
	// state on screen is in motion.
	refreshing bool
	spinner    spinner.Model

	// detailOpen drives the `i` host-detail modal. Cleared on `i`
	// or `esc`. The overlay reads from Selected() so the App
	// doesn't need to thread a separate "which host" argument.
	detailOpen bool

	// Telegram bridge status, pushed in by App from config. The status
	// line surfaces whether the bridge is on and how many chats are
	// paired; `T` mints a pairing code (shown as a toast).
	tgEnabled bool
	tgPaired  int
}

func newNetwork(st styles.Styles, km Keymap) networkModel {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = lipgloss.NewStyle().Foreground(st.Semantic.Info)
	return networkModel{st: st, km: km, spinner: sp}
}

// SetHosts mirrors the Sessions/Dashboard pattern — the App pushes
// the latest hosts list in via this setter after each refresh tick.
//
// Side effect: clears the refreshing flag so the spinner column
// reverts to chips once fresh state lands.
func (m *networkModel) SetHosts(hs []hostStatus) {
	m.hosts = hs
	if m.cursor >= len(hs) {
		m.cursor = max0(len(hs) - 1)
	}
	m.refreshing = false
}

// SetVersion records the local ccmux build version so renderRow can
// flag peers running a different ccmuxd build with the `[↑ update]`
// chip. Empty disables the chip globally.
func (m *networkModel) SetVersion(v string) { m.version = v }

// SetTelegram records the Telegram bridge status (from config) for the
// status line. paired is the count of enrolled chats.
func (m *networkModel) SetTelegram(enabled bool, paired int) {
	m.tgEnabled = enabled
	m.tgPaired = paired
}

// telegramStatusLine renders the one-line Telegram bridge summary shown
// under the Network header.
func (m networkModel) telegramStatusLine() string {
	st := m.st
	if !m.tgEnabled {
		return st.Muted.Render("Telegram: off — ") + st.Key.Render("ccmux telegram register") +
			st.Muted.Render(" to control ccmux from your phone")
	}
	var chat string
	switch {
	case m.tgPaired == 0:
		chat = "no chats paired"
	case m.tgPaired == 1:
		chat = "1 chat paired"
	default:
		chat = fmt.Sprintf("%d chats paired", m.tgPaired)
	}
	return st.Muted.Render("Telegram: on · "+chat+" · ") + st.Key.Render("T") + st.Muted.Render(" pair")
}

// StartRefresh flags the screen as "refresh in flight" so the chip
// column renders the spinner until SetHosts settles new state, and
// returns the tea.Cmd that starts the spinner ticking. Caller is
// expected to batch this with the actual refresh command.
func (m *networkModel) StartRefresh() tea.Cmd {
	m.refreshing = true
	return m.spinner.Tick
}

// HelpBarProps returns the screen-specific key hints for the
// Network screen. `s setup ssh` advertises the SSH setup wizard
// keybind; `i details` advertises the host-detail overlay.
func (m networkModel) HelpBarProps(width int) components.HelpBarProps {
	return components.HelpBarProps{
		Hints: []components.KeyHint{
			{Key: "?", Label: "help", Priority: 10},
			{Key: "q", Label: "quit", Priority: 10},
			{Key: "enter", Label: "ssh", Priority: 8},
			{Key: "s", Label: "setup ssh", Priority: 7},
			{Key: "i", Label: "details", Priority: 5},
			{Key: "T", Label: "telegram", Priority: 4},
			{Key: "r", Label: "refresh", Priority: 4},
			{Key: "1-7", Label: "screens", Priority: 2},
		},
		Width: width,
	}
}

func (m networkModel) Update(msg tea.Msg) (networkModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case keyMatches(msg, m.km.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case keyMatches(msg, m.km.Down):
			if m.cursor < len(m.hosts)-1 {
				m.cursor++
			}
		}
	case spinner.TickMsg:
		// Keep the spinner ticking whenever we're refreshing. Once
		// SetHosts clears the flag, we stop returning a follow-up
		// tick and the animation halts.
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if !m.refreshing {
			return m, nil
		}
		return m, cmd
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

// OpenDetail flips the host-detail overlay on. No-op when no row is
// selectable; the caller is responsible for that guard.
func (m *networkModel) OpenDetail() { m.detailOpen = true }

// CloseDetail flips the host-detail overlay off.
func (m *networkModel) CloseDetail() { m.detailOpen = false }

// DetailOpen reports whether the `i` overlay is currently rendered.
// Used by App.modalCapturingText / overlay routing.
func (m networkModel) DetailOpen() bool { return m.detailOpen }

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

	// Header: just the title and a small device count. The inline
	// action hint that used to live here is now in the HelpBar —
	// avoid duplicating the key vocabulary across two places.
	header := st.Emphasis.Render("Network") + "  " +
		st.Muted.Render(fmt.Sprintf("(%d)", len(m.hosts)))

	if len(m.hosts) == 0 {
		parts := []string{header, m.telegramStatusLine(), "", st.Muted.Render("No devices discovered yet.")}
		// One-sentence hint about the tailnet dependency — kept on
		// wide because the empty state is rare and the user benefits
		// from the pointer. The legend / glossary block is gone:
		// chips elsewhere on the screen carry that meaning.
		if !narrow {
			parts = append(parts,
				"",
				"This screen lists every machine on your tailnet that ccmux can see.",
				"Make sure tailscale is signed in ("+st.Key.Render("tailscale status")+") and try "+st.Key.Render("r")+" to refresh.",
			)
		}
		return st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(strings.Join(parts, "\n"))
	}

	rows := []string{header, m.telegramStatusLine(), ""}

	// Source-grouped layout. Sections render in a pinned order;
	// empty sections are skipped so the screen doesn't carry blank
	// headings. Cursor index is computed against the unsorted hosts
	// slice; we walk in source-order and render each row alongside
	// its original index so cursor selection still tracks.
	sectionOrder := []struct {
		source string
		label  string
	}{
		{"local", "Local"},
		{"configured", "Configured"},
		{"discovered", "Discovered"},
		{"mobile", "Mobile"},
	}
	first := true
	for _, sec := range sectionOrder {
		idxs := indicesForSource(m.hosts, sec.source)
		if len(idxs) == 0 {
			continue
		}
		if !first {
			rows = append(rows, "")
		}
		first = false
		rows = append(rows, st.Type.Subtitle.Render(sec.label))
		for _, i := range idxs {
			rows = append(rows, m.renderRow(m.hosts[i], i == m.cursor, narrow))
		}
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

// indicesForSource returns the indices in `hs` whose Source matches
// `want`. Preserves the slice's original order so within a section
// rows render in the same sequence the refresh loop produced.
func indicesForSource(hs []hostStatus, want string) []int {
	var out []int
	for i, h := range hs {
		if h.Source == want {
			out = append(out, i)
		}
	}
	return out
}

func (m networkModel) renderRow(h hostStatus, selected, narrow bool) string {
	st := m.st
	icon := iconForHost(h, st)
	name := h.Name
	if h.Local {
		name += "  " + st.Muted.Render("(this device)")
	}
	// Chip column: spinner while a refresh is in flight; the
	// resolved chip set once state lands.
	var chipStr string
	if m.refreshing {
		chipStr = m.spinner.View()
	} else {
		chipStr = m.renderChips(h, narrow)
	}
	// Indent rows under their section heading by one design-system
	// step (2 cells) — the spec's grouping rule.
	indent := strings.Repeat(" ", st.Spacing.MD)
	row := fmt.Sprintf("%s%s %s    %s", indent, icon, name, chipStr)
	if selected {
		row = st.ListItemSelected.Render(row)
	}
	return row
}

// renderChips builds the per-row status chip set from a hostStatus.
// Multiple chips can appear; they're rendered left-to-right after
// the host name. The local row deliberately omits `[↑ update]` and
// `[SSH ✓]` — neither is meaningful for the device you're on.
//
// Narrow terminals collapse the chip vocabulary to glyph-only
// shorthand (✓ / ↑ / ✗ / …) so the row still fits on a phone width.
func (m networkModel) renderChips(h hostStatus, narrow bool) string {
	st := m.st
	var chips []string

	switch {
	case h.Mobile:
		chips = append(chips, chipText(st, st.Muted, "Moshi", "📱", narrow))
	case h.NeedsInstall:
		chips = append(chips, chipText(st, st.Muted, "no ccmuxd", "○", narrow))
	case !h.OK && !h.Local && h.Err != nil:
		chips = append(chips, chipText(st, st.StateError, "unreachable", "✗", narrow))
	}

	if h.TailscaleSSH && !h.Local && !h.Mobile {
		chips = append(chips, chipText(st, st.StatusGood, "Tailscale SSH ✓", "ts✓", narrow))
	}
	if h.SSHVerified && !h.Local && !h.Mobile {
		chips = append(chips, chipText(st, st.StatusGood, "SSH ✓", "✓", narrow))
	}

	if !h.Local && !h.Mobile && !h.NeedsInstall &&
		h.Version != "" && m.version != "" && versionsDiffer(m.version, h.Version) {
		chips = append(chips, chipText(st, st.StatusWarning, "↑ update", "↑", narrow))
	}

	return strings.Join(chips, " ")
}

// chipText renders one chip with bracketed long form on wide and a
// terse glyph on narrow. The style argument carries the chip's
// semantic color (success / warning / muted / error).
func chipText(_ styles.Styles, style lipgloss.Style, long, short string, narrow bool) string {
	if narrow {
		return style.Render(short)
	}
	return style.Render("[" + long + "]")
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

// renderDetailOverlay produces the full host-detail modal opened by
// pressing `i` on the Network screen. Parallel to the dashboard's
// `u` usage overlay — same place-in-center, same close-on-key
// hint.
func (m networkModel) renderDetailOverlay(width, height int) string {
	st := m.st
	sel := m.Selected()
	if sel == nil {
		body := st.Muted.Render("(no host selected)")
		modal := st.PaneFocused.Width(40).Render(body)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
	}

	lines := []string{
		st.Emphasis.Render("Host detail · " + sel.Name),
		st.Subtitle.Render("Pressed-i expansion of the Network row — tailnet IP, ccmuxd version, SSH status, and last-probe timestamp."),
		"",
	}

	lines = append(lines, st.Subtitle.Render("Identity"))
	lines = append(lines, fmt.Sprintf("  name             %s", sel.Name))
	if sel.OS != "" {
		lines = append(lines, fmt.Sprintf("  os               %s", sel.OS))
	}
	if sel.Source != "" {
		lines = append(lines, fmt.Sprintf("  source           %s", sel.Source))
	}
	lines = append(lines, "")

	lines = append(lines, st.Subtitle.Render("Network"))
	if sel.Address != "" {
		lines = append(lines, fmt.Sprintf("  tailnet address  %s", sel.Address))
	}
	if sel.DialHost != "" {
		lines = append(lines, fmt.Sprintf("  dial host        %s", sel.DialHost))
	}
	if sel.SSHPort > 0 {
		lines = append(lines, fmt.Sprintf("  ssh port         %d", sel.SSHPort))
	}
	lines = append(lines, "")

	lines = append(lines, st.Subtitle.Render("Daemon"))
	if sel.Version != "" {
		lines = append(lines, fmt.Sprintf("  ccmuxd version   %s", sel.Version))
	} else if sel.NeedsInstall {
		lines = append(lines, "  ccmuxd version   "+st.Muted.Render("(not installed)"))
	} else {
		lines = append(lines, "  ccmuxd version   "+st.Muted.Render("(unknown)"))
	}
	lines = append(lines, fmt.Sprintf("  sessions         %d", sel.Sessions))
	if !sel.LastProbe.IsZero() {
		lines = append(lines, fmt.Sprintf("  last probe       %s",
			sel.LastProbe.Local().Format("15:04:05")))
	}
	lines = append(lines, "")

	lines = append(lines, st.Subtitle.Render("SSH"))
	switch {
	case sel.Local:
		lines = append(lines, "  status           "+st.Muted.Render("(local — n/a)"))
	case sel.Mobile:
		lines = append(lines, "  status           "+st.Muted.Render("(mobile — Moshi handles auth)"))
	case sel.TailscaleSSH:
		lines = append(lines, "  status           "+st.StatusGood.Render("Tailscale SSH ✓"))
	case sel.SSHVerified:
		lines = append(lines, "  status           "+st.StatusGood.Render("key verified"))
	default:
		lines = append(lines, "  status           "+st.Muted.Render("(unverified — press s to set up)"))
	}
	lines = append(lines, "")

	lines = append(lines, st.Muted.Render("press i or esc to close"))

	modalW := minInt(72, width-4)
	body := strings.Join(lines, "\n")
	modal := st.PaneFocused.Width(modalW).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}
