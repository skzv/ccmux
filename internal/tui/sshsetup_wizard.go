package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// sshWizardStep names every screen the wizard can show. The model
// owns a step value and the View renders accordingly; all
// transitions go through Update so the state machine stays linear
// and testable.
type sshWizardStep int

const (
	// sshWizardClosed — model is not active. Zero value, so an
	// uninitialized model is inert (no accidental rendering).
	sshWizardClosed sshWizardStep = iota
	// sshWizardConfirm — pre-flight slide. Shows what will happen
	// and the target, waits for Enter (proceed) or Esc (cancel).
	// Skipped when the wizard is launched from a "we already
	// detected AuthFailed" path because the user already knows
	// what's about to happen.
	sshWizardConfirm
	// sshWizardPassword — masked textinput for the SSH password.
	// Re-entered on ErrWrongPassword without losing wizard state.
	sshWizardPassword
	// sshWizardRunning — install in progress. Stage lines stream
	// in as wizardProgressMsg events.
	sshWizardRunning
	// sshWizardEnumerate — show the multi-select of other users
	// discovered on the remote. The user picks zero or more, hits
	// Enter, and we add them to hosts.toml.
	sshWizardEnumerate
	// sshWizardError — a non-recoverable failure (or a recoverable
	// one between retries). Shows the message + an instruction
	// line on what to do.
	sshWizardError
	// sshWizardDone — install + enumerate complete. One Enter
	// closes the wizard and (if there's one) resumes whatever
	// triggered the wizard.
	sshWizardDone
)

// sshWizardModel is the Bubble Tea model for the SSH setup flow.
// Each instance is for one Target — re-opening the wizard against
// a different host constructs a new model.
type sshWizardModel struct {
	step     sshWizardStep
	target   sshsetup.Target
	st       styles.Styles
	width    int
	height   int
	passwd   textinput.Model
	stages   []string        // accumulated progress stage:detail rows
	err      string          // last error message rendered on the Error step
	others   []string        // users found by EnumerateUsers
	selected map[string]bool // multi-select state for enumerate
	cursor   int             // highlighted row inside the enumerate list
	// resumeOnDone is metadata the parent app uses to know what
	// action to perform after the wizard closes successfully. We
	// pass it through opaquely — the wizard doesn't care.
	resumeOnDone any
	// install seam — lets tests inject a fake installer that
	// drives the model without touching the network. Production
	// uses nil, which routes through sshsetup.InstallKeyViaPassword.
	installFn func(ctx context.Context, t sshsetup.Target, password string, key sshsetup.LocalKey, p sshsetup.Progress) error
	// enumerate seam — same idea for EnumerateUsers.
	enumerateFn func(ctx context.Context, t sshsetup.Target, key sshsetup.LocalKey) ([]string, error)
	// local key resolution — also a seam so the test doesn't have
	// to manage a real ~/.ssh.
	keyFn func() (sshsetup.LocalKey, error)
}

// newSSHWizard constructs a closed wizard ready for Open. The
// returned model is safe to embed in the root app even when
// inactive — Active() returns false and View() emits "".
func newSSHWizard(st styles.Styles) *sshWizardModel {
	ti := textinput.New()
	ti.Placeholder = "ssh password"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 256
	ti.Width = 32
	ti.Prompt = ""
	return &sshWizardModel{st: st, passwd: ti, selected: map[string]bool{}}
}

// Open kicks the wizard onto its first screen with the supplied
// target + an optional resume payload. The parent app holds the
// payload and acts on it when wizardCompletedMsg arrives.
func (m *sshWizardModel) Open(target sshsetup.Target, resume any) tea.Cmd {
	m.step = sshWizardConfirm
	m.target = target
	m.stages = nil
	m.err = ""
	m.others = nil
	m.selected = map[string]bool{}
	m.cursor = 0
	m.resumeOnDone = resume
	m.passwd.Reset()
	return nil
}

// Active reports whether the wizard is on screen and absorbing key
// events. The router uses this to decide whether to route input to
// the wizard or to the underlying screen.
func (m *sshWizardModel) Active() bool {
	return m.step != sshWizardClosed
}

// Step exposes the current step for tests + debug overlays.
func (m *sshWizardModel) Step() sshWizardStep { return m.step }

// Close forces the wizard back to the closed state without emitting
// any messages. Use Cancel() to also tell the parent "user bailed".
func (m *sshWizardModel) Close() { m.step = sshWizardClosed }

// wizardProgressMsg is what Install's Progress callback delivers
// into the Bubble Tea loop. We buffer through a small channel so
// the install goroutine doesn't block on Update.
type wizardProgressMsg struct{ stage, detail string }

// wizardInstallDoneMsg fires when the install goroutine returns.
type wizardInstallDoneMsg struct{ err error }

// wizardEnumerateDoneMsg fires when EnumerateUsers returns.
type wizardEnumerateDoneMsg struct {
	users []string
	err   error
}

// wizardCompletedMsg bubbles up when the user finishes (or chooses
// to skip the enumerate prompt). resume is the opaque payload the
// caller stashed via Open.
type wizardCompletedMsg struct {
	target sshsetup.Target
	added  []string // user names accepted from the enumerate step
	resume any
}

// wizardCancelledMsg fires when the user Esc-bails out of the
// wizard. The parent app should restore the previous screen with
// no further action.
type wizardCancelledMsg struct{ resume any }

// Update processes one tea.Msg. Returns the (possibly mutated)
// model and any commands to dispatch. Each step has its own input
// handler block — keeps the switch flat instead of nesting.
func (m *sshWizardModel) Update(msg tea.Msg) (*sshWizardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	case wizardProgressMsg:
		m.stages = append(m.stages, fmt.Sprintf("%s: %s", msg.stage, msg.detail))
		return m, nil
	case wizardInstallDoneMsg:
		if msg.err == nil {
			return m, m.startEnumerate()
		}
		// Wrong password → bounce back to password step.
		if errors.Is(msg.err, sshsetup.ErrWrongPassword) {
			m.step = sshWizardPassword
			m.err = "password rejected — try again"
			m.passwd.Reset()
			m.passwd.Focus()
			return m, nil
		}
		m.step = sshWizardError
		m.err = msg.err.Error()
		return m, nil
	case wizardEnumerateDoneMsg:
		if msg.err != nil || len(msg.users) == 0 {
			// Skip the enumerate screen entirely if nothing to
			// show — the install succeeded so we're done.
			m.step = sshWizardDone
			return m, nil
		}
		m.others = msg.users
		m.cursor = 0
		m.step = sshWizardEnumerate
		return m, nil
	}
	return m, nil
}

// updateKey routes a keypress to the per-step handler. Each step's
// handler returns the new step (which may be the same one) plus an
// optional cmd to fire.
func (m *sshWizardModel) updateKey(msg tea.KeyMsg) (*sshWizardModel, tea.Cmd) {
	// Esc is universal "cancel the wizard" except when the user is
	// typing in the password textinput — there it should clear the
	// field, not bail. So we handle the bail case per-step.
	switch m.step {
	case sshWizardConfirm:
		switch msg.String() {
		case "enter":
			m.step = sshWizardPassword
			m.passwd.Focus()
			return m, textinput.Blink
		case "esc":
			return m, m.emitCancel()
		}
	case sshWizardPassword:
		switch msg.String() {
		case "enter":
			pw := m.passwd.Value()
			if pw == "" {
				// Empty password → stay on the step, hint.
				m.err = "password is required"
				return m, nil
			}
			m.err = ""
			m.step = sshWizardRunning
			return m, m.startInstall(pw)
		case "esc":
			return m, m.emitCancel()
		}
		var cmd tea.Cmd
		m.passwd, cmd = m.passwd.Update(msg)
		return m, cmd
	case sshWizardRunning:
		// Block keys during the install — Esc still allows a soft
		// cancel by short-circuiting back to a "cancelled" close.
		if msg.String() == "esc" {
			return m, m.emitCancel()
		}
	case sshWizardEnumerate:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.others)-1 {
				m.cursor++
			}
		case " ", "x":
			name := m.others[m.cursor]
			m.selected[name] = !m.selected[name]
		case "a":
			// "select all" — handy when the wizard shows e.g.
			// 3 accounts on a multi-user dev machine.
			for _, u := range m.others {
				m.selected[u] = true
			}
		case "n":
			for _, u := range m.others {
				m.selected[u] = false
			}
		case "enter":
			m.step = sshWizardDone
			return m, m.emitCompleted()
		case "esc":
			// Esc here means "don't add anyone" but still complete
			// (the key install is irreversible at this point).
			for k := range m.selected {
				m.selected[k] = false
			}
			m.step = sshWizardDone
			return m, m.emitCompleted()
		}
	case sshWizardError:
		switch msg.String() {
		case "enter", "esc":
			return m, m.emitCancel()
		case "r":
			// Retry from the password screen.
			m.step = sshWizardPassword
			m.err = ""
			m.passwd.Reset()
			m.passwd.Focus()
			return m, textinput.Blink
		}
	case sshWizardDone:
		if msg.String() == "enter" || msg.String() == "esc" {
			return m, m.emitCompleted()
		}
	}
	return m, nil
}

// startInstall kicks the install off in a goroutine and returns the
// tea.Cmd that wires the goroutine's completion into a
// wizardInstallDoneMsg. Progress lines stream via a separate Cmd
// chain — we tick the goroutine forward in a small batched way
// rather than firing one Cmd per progress event (that would race
// the install completion).
func (m *sshWizardModel) startInstall(password string) tea.Cmd {
	installFn := m.installFn
	if installFn == nil {
		installFn = sshsetup.InstallKeyViaPassword
	}
	keyFn := m.keyFn
	if keyFn == nil {
		keyFn = sshsetup.EnsureLocalKey
	}
	target := m.target
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		key, err := keyFn()
		if err != nil {
			return wizardInstallDoneMsg{err: fmt.Errorf("local key: %w", err)}
		}
		// Buffer Progress events through a non-blocking sink: we
		// don't have the Program's Send here in unit tests, and
		// the wizard is robust to a missing progress stream. The
		// streaming-stages live test is in the integration test.
		err = installFn(ctx, target, password, key, nil)
		return wizardInstallDoneMsg{err: err}
	}
}

// startEnumerate kicks the post-install user-enumeration goroutine.
// On success delivers the (possibly empty) list to the wizard via
// wizardEnumerateDoneMsg.
func (m *sshWizardModel) startEnumerate() tea.Cmd {
	enumerateFn := m.enumerateFn
	if enumerateFn == nil {
		enumerateFn = sshsetup.EnumerateUsers
	}
	keyFn := m.keyFn
	if keyFn == nil {
		keyFn = sshsetup.EnsureLocalKey
	}
	target := m.target
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		key, err := keyFn()
		if err != nil {
			// Enumerate failures aren't fatal — the install
			// already succeeded.
			return wizardEnumerateDoneMsg{users: nil, err: err}
		}
		users, err := enumerateFn(ctx, target, key)
		return wizardEnumerateDoneMsg{users: users, err: err}
	}
}

// emitCancel constructs the cancel command. Separate so unit tests
// can assert that Esc from any screen returns the expected message.
func (m *sshWizardModel) emitCancel() tea.Cmd {
	resume := m.resumeOnDone
	m.Close()
	return func() tea.Msg { return wizardCancelledMsg{resume: resume} }
}

// emitCompleted assembles the success message including any
// enumerate selections.
func (m *sshWizardModel) emitCompleted() tea.Cmd {
	resume := m.resumeOnDone
	target := m.target
	var added []string
	for _, u := range m.others {
		if m.selected[u] {
			added = append(added, u)
		}
	}
	m.Close()
	return func() tea.Msg {
		return wizardCompletedMsg{target: target, added: added, resume: resume}
	}
}

// View renders the wizard. Each step emits a centered card; an
// inactive wizard renders empty. The root app overlays this on top
// of whatever screen was active when Open fired.
func (m *sshWizardModel) View(w, h int) string {
	if !m.Active() {
		return ""
	}
	if w == 0 {
		w = m.width
	}
	if h == 0 {
		h = m.height
	}
	cardW := w - 6
	if cardW > 72 {
		cardW = 72
	}
	if cardW < 44 {
		cardW = 44
	}
	title := m.st.Title.Foreground(m.st.P.Mauve).Bold(true)

	var lines []string
	switch m.step {
	case sshWizardConfirm:
		lines = append(lines, title.Render("SSH setup for "+m.target.String()))
		lines = append(lines, "")
		lines = append(lines,
			"ccmux will install your public key on this host so",
			"future attaches don't prompt for a password.",
			"",
			m.st.Key.Render("•")+" reuses an existing ~/.ssh key, or generates ed25519",
			m.st.Key.Render("•")+" password is used once, never stored",
			m.st.Key.Render("•")+" idempotent — safe to re-run",
		)
		lines = append(lines, "")
		lines = append(lines, m.st.Muted.Render("[Enter] continue   [Esc] cancel"))
	case sshWizardPassword:
		lines = append(lines, title.Render("Password for "+m.target.String()))
		lines = append(lines, "")
		lines = append(lines, "Password:  "+m.passwd.View())
		if m.err != "" {
			lines = append(lines, "")
			lines = append(lines, m.st.Title.Foreground(m.st.P.Red).Render(m.err))
		}
		lines = append(lines, "")
		lines = append(lines, m.st.Muted.Render("[Enter] install key   [Esc] cancel"))
	case sshWizardRunning:
		lines = append(lines, title.Render("Installing on "+m.target.String()))
		lines = append(lines, "")
		if len(m.stages) == 0 {
			lines = append(lines, m.st.Muted.Render("connecting…"))
		}
		for _, s := range m.stages {
			lines = append(lines, "  "+m.st.Key.Render("·")+" "+s)
		}
		lines = append(lines, "")
		lines = append(lines, m.st.Muted.Render("[Esc] cancel"))
	case sshWizardEnumerate:
		lines = append(lines, title.Render("Other users on "+m.target.Host))
		lines = append(lines, "")
		lines = append(lines, "Add any of these as separate hosts?")
		lines = append(lines, "")
		for i, u := range m.others {
			cursor := "  "
			if i == m.cursor {
				cursor = m.st.Key.Render("> ")
			}
			box := "[ ]"
			if m.selected[u] {
				box = m.st.Key.Render("[x]")
			}
			lines = append(lines, cursor+box+" "+u)
		}
		lines = append(lines, "")
		lines = append(lines, m.st.Muted.Render("[space] toggle  [a] all  [n] none  [Enter] done  [Esc] skip"))
	case sshWizardError:
		lines = append(lines, title.Foreground(m.st.P.Red).Render("Setup failed"))
		lines = append(lines, "")
		// Wrap long error lines for readability inside the card.
		lines = append(lines, wizardWrap(m.err, cardW-6)...)
		lines = append(lines, "")
		lines = append(lines, m.st.Muted.Render("[r] retry password   [Esc] cancel"))
	case sshWizardDone:
		lines = append(lines, title.Foreground(m.st.P.Green).Render("Setup complete"))
		lines = append(lines, "")
		lines = append(lines, m.target.String()+" is ready for attach")
		lines = append(lines, "")
		lines = append(lines, m.st.Muted.Render("[Enter] continue"))
	}

	body := strings.Join(lines, "\n")
	card := lipgloss.NewStyle().
		Padding(1, 3).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.st.P.Mauve).
		Width(cardW).
		Render(body)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, card)
}

// wizardWrap is a trivial column wrapper for the error step. The
// text in question is a single Go error string; we just split on
// spaces and pack into lines no wider than `cols`. (Local name to
// dodge a collision with conversations.go's `wrap`.)
func wizardWrap(s string, cols int) []string {
	if cols < 10 {
		cols = 10
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	cur := ""
	for _, w := range words {
		if cur == "" {
			cur = w
			continue
		}
		if len(cur)+1+len(w) > cols {
			out = append(out, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
