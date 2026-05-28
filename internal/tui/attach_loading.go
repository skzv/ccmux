package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/tmuxchrome"
)

// Attach-loading overlay. Shown between the moment the user picks a
// session/conversation and the moment Bubble Tea suspends to run
// `tmux attach`. Without it, the TUI just sits there during the
// Moshi-detect + chrome-apply prep (≤7s budget) and the agent's own
// boot time on resume, leaving the user wondering whether Enter
// registered at all.

type attachKind int

const (
	attachKindAttach  attachKind = iota // attaching to an already-running session
	attachKindResume                    // resuming a past conversation (spawns + attaches)
	attachKindNew                       // starting a fresh session for a project
	attachKindRemote                    // ssh/mosh into a remote tmux session
	attachKindOpening                   // gathering a project's sessions + conversations before showing the menu
)

// attachState is the model field that drives the overlay.
type attachState struct {
	active    bool
	kind      attachKind
	label     string    // human-readable target (project label or session name)
	startedAt time.Time // for elapsed-time display
	spinFrame int       // current spinner frame index
}

// attachReadyMsg fires after the off-thread Moshi-detect + tmuxchrome.Apply
// prep finishes; carries everything App needs to actually call tea.ExecProcess.
type attachReadyMsg struct {
	Session      string
	Nested       bool
	DetachOthers bool
}

// attachExitedMsg fires when the suspended tmux subprocess returns
// control. Centralizes the "clear the overlay + refresh + maybe toast"
// path so every attach callsite uses the same plumbing.
type attachExitedMsg struct {
	Err error
	// RemoteSSHTarget pins the user@host:port we just tried to
	// ssh/mosh into. When non-nil AND Err looks like a permission
	// failure, the App swaps the error toast for an SSH setup
	// wizard pointed at this target. Local attaches leave nil.
	RemoteSSHTarget *attachRemoteTarget
}

// attachRemoteTarget mirrors sshsetup.Target shape. Lives here so
// attachExitedMsg doesn't pull sshsetup into the attach-loading
// file — the App converts to sshsetup.Target at the routing site.
type attachRemoteTarget struct {
	User string
	Host string
	Port int
	// TailscaleSSH marks a peer where Tailscale handles SSH auth via
	// the tailnet identity. When set, the post-attach auth-failure
	// auto-route does NOT open the SSH setup wizard — installing a
	// key wouldn't fix a Tailscale-SSH rejection (that's an ACL
	// issue), so we surface a more targeted hint instead.
	TailscaleSSH bool
	// OpenShellOnSetup marks the "open an interactive shell" intent
	// (Network-tab Enter) vs a session/project attach. When the
	// post-attach auth-failure auto-route opens the SSH wizard, a
	// true value tells the wizard to drop the user into a shell on
	// success; false leaves them to retry their attach.
	OpenShellOnSetup bool
}

// attachSpinTickMsg advances the spinner frame while the overlay is up.
type attachSpinTickMsg struct{}

// spinner frames — braille dots, same set Bubble's spinner.Dot uses.
var attachSpinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const attachSpinInterval = 90 * time.Millisecond

// startAttaching mutates `a` to flip the overlay on and returns the
// spinner-tick cmd. Caller is expected to tea.Batch this with whatever
// real work kicks off the attach (prep cmd, tmux.New, ssh exec, …).
func (a *App) startAttaching(kind attachKind, label string) tea.Cmd {
	a.attach = attachState{
		active:    true,
		kind:      kind,
		label:     label,
		startedAt: time.Now(),
		spinFrame: 0,
	}
	return attachSpinTickCmd()
}

// stopAttaching clears the overlay state. Idempotent.
func (a *App) stopAttaching() {
	a.attach = attachState{}
}

func attachSpinTickCmd() tea.Cmd {
	return tea.Tick(attachSpinInterval, func(time.Time) tea.Msg { return attachSpinTickMsg{} })
}

// prepLocalAttachCmd does the Moshi-status probe and applies ccmux's
// chrome to the target tmux session, then emits attachReadyMsg so the
// App can suspend Bubble Tea and exec `tmux attach`. Both probes are
// best-effort (chrome failure → vanilla tmux styling, Moshi failure →
// no phone badge); their errors are intentionally swallowed.
//
// Note that this runs OFF the Update goroutine — that's the whole
// point of routing through a tea.Cmd. The previous shape did this
// work synchronously in Update, which froze the TUI for up to ~7s on
// every attach.
func prepLocalAttachCmd(session, projectLabel string, detachOthers bool) tea.Cmd {
	return func() tea.Msg {
		mctx, mcancel := context.WithTimeout(context.Background(), 2*time.Second)
		mst := moshi.Detect(mctx)
		mcancel()
		nested := tmuxchrome.InTmux()
		reachable := mst.Paired && mst.HooksInstalled && mst.ServiceRunning
		cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = tmuxchrome.Apply(cctx, session, projectLabel, reachable, nested)
		ccancel()
		return attachReadyMsg{
			Session:      session,
			Nested:       nested,
			DetachOthers: detachOthers,
		}
	}
}

// renderAttachingOverlay returns the centered loading screen shown
// while an attach is in flight. Reads the spinner frame + start time
// from a.attach.
func (a App) renderAttachingOverlay(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	verb := attachVerb(a.attach.kind)
	target := a.attach.label
	if target == "" {
		target = "session"
	}
	frame := attachSpinFrames[a.attach.spinFrame%len(attachSpinFrames)]
	spin := lipgloss.NewStyle().Foreground(a.styles.P.Mauve).Bold(true).Render(frame)
	title := lipgloss.NewStyle().Foreground(a.styles.P.Lavender).Bold(true).
		Render(fmt.Sprintf("%s %s", verb, target))
	hint := a.styles.Muted.Render(attachHint(a.attach.kind))

	// Elapsed timer — only shown after the first second so it doesn't
	// flicker "0s" on quick attaches.
	var elapsed string
	if !a.attach.startedAt.IsZero() {
		d := time.Since(a.attach.startedAt)
		if d >= time.Second {
			elapsed = a.styles.Muted.Render(fmt.Sprintf("%ds", int(d.Seconds())))
		}
	}

	header := lipgloss.JoinHorizontal(lipgloss.Center, spin, "  ", title)
	rows := []string{header, "", hint}
	if elapsed != "" {
		rows = append(rows, "", elapsed)
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(a.styles.P.Mauve).
		Padding(1, 3).
		Render(lipgloss.JoinVertical(lipgloss.Center, rows...))

	return lipgloss.Place(width, height,
		lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
	)
}

func attachVerb(k attachKind) string {
	switch k {
	case attachKindResume:
		return "Resuming"
	case attachKindNew:
		return "Starting"
	case attachKindRemote:
		return "Connecting to"
	case attachKindOpening:
		return "Opening"
	default:
		return "Attaching to"
	}
}

func attachHint(k attachKind) string {
	switch k {
	case attachKindResume:
		return "loading conversation history…"
	case attachKindNew:
		return "spawning agent…"
	case attachKindRemote:
		return "negotiating ssh/mosh…"
	case attachKindOpening:
		return "listing sessions and conversations…"
	default:
		return "applying chrome and attaching…"
	}
}
