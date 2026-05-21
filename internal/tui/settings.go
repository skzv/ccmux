package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// settingsModel surfaces ccmux's own config.toml with toggles and links,
// plus a Moshi/moshi-hook status block at the top so users can see at a
// glance whether the mobile push pipeline is set up.
//
// Editing model (added in v0.1.x): a cursor moves over the editable
// fields (projects.root, scaffold.dirs, subscription.tier). Enter
// opens an inline textinput; Enter again commits, Esc cancels.
// Multi-line fields (initial_prompt, gitignore_body) launch $EDITOR
// against ~/.config/ccmux/config.toml so the user gets a real editor
// for the prose-heavy bits.
type settingsModel struct {
	st         styles.Styles
	km         Keymap
	cfg        config.Config
	version    string
	moshiState moshi.Status
	moshiCheck time.Time

	cursor  int // index into editableFields()
	editing bool
	editor  textinput.Model
	errMsg  string
	saveMsg string // transient "saved ✓" message
	savedAt time.Time
}

// editableField is one row the user can move the cursor onto. The
// get/set closures let us model both plain strings (projects.root) and
// derived shapes (scaffold.dirs serialized as comma-separated text).
type editableField struct {
	label string

	// Section + key in TOML, for the help-line hint.
	hint string

	// get reads the current value as a single-line string.
	get func(c *config.Config) string

	// set parses the input, validates, and applies to the config.
	// Returns a human-readable error to display inline on failure.
	set func(c *config.Config, raw string) error

	// validateOnly is true for read-only display rows that participate
	// in the cursor but aren't editable. Useful for showing computed
	// state alongside the editable knobs.
	readOnly bool

	// options, when non-empty, turns the row into a cycle-picker:
	// pressing Enter advances to the next value (wrapping at the end)
	// and saves immediately, instead of opening the inline textinput.
	// For a fixed enum like the agent list, cycling beats making the
	// user type the exact string.
	options []string
}

func editableFields() []editableField {
	return []editableField{
		{
			label: "projects.root",
			hint:  "Where ccmux looks for projects (~/Projects default).",
			get:   func(c *config.Config) string { return c.Projects.Root },
			set: func(c *config.Config, raw string) error {
				raw = strings.TrimSpace(raw)
				if raw == "" {
					return fmt.Errorf("must be a path")
				}
				if strings.HasPrefix(raw, "~/") {
					home, err := os.UserHomeDir()
					if err != nil {
						return err
					}
					raw = home + raw[1:]
				}
				if fi, err := os.Stat(raw); err != nil {
					return fmt.Errorf("path doesn't exist: %s", raw)
				} else if !fi.IsDir() {
					return fmt.Errorf("not a directory: %s", raw)
				}
				c.Projects.Root = raw
				return nil
			},
		},
		{
			label: "scaffold.dirs",
			hint:  "Comma-separated. Empty = default (docs/01_Specs, docs/02_Architecture, docs/03_Agent_Logs).",
			get: func(c *config.Config) string {
				if len(c.Scaffold.Dirs) == 0 {
					return ""
				}
				return strings.Join(c.Scaffold.Dirs, ", ")
			},
			set: func(c *config.Config, raw string) error {
				raw = strings.TrimSpace(raw)
				if raw == "" {
					c.Scaffold.Dirs = nil
					return nil
				}
				parts := strings.Split(raw, ",")
				var out []string
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p == "" {
						continue
					}
					if strings.HasPrefix(p, "/") {
						return fmt.Errorf("paths must be relative, got %q", p)
					}
					out = append(out, p)
				}
				if len(out) == 0 {
					return fmt.Errorf("no valid entries (separate with commas)")
				}
				c.Scaffold.Dirs = out
				return nil
			},
		},
		{
			label: "subscription.tier",
			hint:  "Drives the dashboard quota bar. One of: api, pro, max5x, max20x.",
			get:   func(c *config.Config) string { return c.Subscription.Tier },
			set: func(c *config.Config, raw string) error {
				raw = strings.TrimSpace(strings.ToLower(raw))
				switch raw {
				case "", "api", "pro", "max5x", "max20x":
					c.Subscription.Tier = raw
					return nil
				}
				return fmt.Errorf("must be one of: api, pro, max5x, max20x")
			},
		},
		{
			label:   "agents.default",
			hint:    "Default agent for new projects and bare sessions. Enter cycles: claude → codex → antigravity → shell.",
			options: []string{"claude", "codex", "antigravity", "shell"},
			get:     func(c *config.Config) string { return c.Agents.Default },
			set: func(c *config.Config, raw string) error {
				raw = strings.TrimSpace(strings.ToLower(raw))
				// Empty = back to claude (the multi-agent default).
				if raw == "" {
					c.Agents.Default = "claude"
					return nil
				}
				// "shell" is the explicit opt-out — bare $SHELL, no agent.
				if raw == "shell" {
					c.Agents.Default = "shell"
					return nil
				}
				// Otherwise must be a known agent ID. ParseID accepts
				// "gemini" as an alias for antigravity, which we want
				// (back-compat for users with old configs in flight),
				// but we normalize to the canonical name on write.
				id, ok := agent.ParseID(raw)
				if !ok {
					return fmt.Errorf("must be one of: claude, codex, antigravity, shell")
				}
				c.Agents.Default = string(id)
				return nil
			},
		},
		{
			label: "sessions.attach_mode",
			hint:  "mirror = other devices stay attached (default); exclusive = attaching detaches them.",
			get: func(c *config.Config) string {
				// Surface the effective value so an empty field reads
				// as "mirror" rather than blank.
				if c.Sessions.AttachMode == "" {
					return "mirror"
				}
				return c.Sessions.AttachMode
			},
			set: func(c *config.Config, raw string) error {
				raw = strings.TrimSpace(strings.ToLower(raw))
				switch raw {
				case "", "mirror":
					c.Sessions.AttachMode = "mirror"
					return nil
				case "exclusive":
					c.Sessions.AttachMode = "exclusive"
					return nil
				}
				return fmt.Errorf("must be 'mirror' or 'exclusive'")
			},
		},
		{
			label: "update.auto_check",
			hint:  "Check for ccmux updates on launch and show a banner. on/off. Never auto-installs.",
			get: func(c *config.Config) string {
				if c.Update.AutoCheck {
					return "on"
				}
				return "off"
			},
			set: func(c *config.Config, raw string) error {
				switch strings.ToLower(strings.TrimSpace(raw)) {
				case "on", "true", "yes", "1", "":
					c.Update.AutoCheck = true
					return nil
				case "off", "false", "no", "0":
					c.Update.AutoCheck = false
					return nil
				}
				return fmt.Errorf("must be 'on' or 'off'")
			},
		},
		{
			label: "theme",
			hint:  "Theme picker UI coming in v0.2. Edit config.toml directly to switch.",
			get:   func(c *config.Config) string { return c.Theme },
			set: func(c *config.Config, raw string) error {
				return fmt.Errorf("not yet editable from the TUI — coming v0.2")
			},
			readOnly: true,
		},
	}
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

	// Editor mode owns the keyboard: enter to commit, esc to cancel.
	if m.editing {
		switch km := msg.(type) {
		case tea.KeyMsg:
			switch km.String() {
			case "esc":
				m.editing = false
				m.errMsg = ""
				return m, nil
			case "enter":
				return m.commit()
			}
		}
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		return m, cmd
	}

	switch km := msg.(type) {
	case tea.KeyMsg:
		fields := editableFields()
		switch km.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			m.errMsg = ""
		case "down", "j":
			if m.cursor < len(fields)-1 {
				m.cursor++
			}
			m.errMsg = ""
		case "enter":
			if m.cursor >= 0 && m.cursor < len(fields) {
				f := fields[m.cursor]
				switch {
				case f.readOnly:
					m.errMsg = "field is read-only: " + f.hint
				case len(f.options) > 0:
					// Cycle-picker row (e.g. agents.default): advance to
					// the next value and persist, no inline editor.
					return m.cycleField(f)
				default:
					m.startEdit(f)
					return m, textinput.Blink
				}
			}
		case "e":
			// Open ~/.config/ccmux/config.toml in $EDITOR for the
			// multi-line fields the TUI can't gracefully inline-edit.
			return m.openEditor()
		}
	}
	return m, nil
}

// startEdit prepares the inline textinput for `f` with the current value.
func (m *settingsModel) startEdit(f editableField) {
	ti := textinput.New()
	ti.SetValue(f.get(&m.cfg))
	ti.Focus()
	ti.CharLimit = 512
	ti.Width = 60
	m.editor = ti
	m.editing = true
	m.errMsg = ""
}

// commit validates the textinput value, applies it to the config, and
// saves to disk. Failures keep the user in edit mode with an inline
// error message; success closes the editor and shows a transient
// "saved ✓" flash.
func (m settingsModel) commit() (settingsModel, tea.Cmd) {
	fields := editableFields()
	if m.cursor < 0 || m.cursor >= len(fields) {
		m.editing = false
		return m, nil
	}
	raw := m.editor.Value()
	if err := fields[m.cursor].set(&m.cfg, raw); err != nil {
		m.errMsg = err.Error()
		return m, nil
	}
	if err := config.Save(m.cfg); err != nil {
		m.errMsg = "save: " + err.Error()
		return m, nil
	}
	m.editing = false
	m.errMsg = ""
	m.saveMsg = "saved ✓"
	m.savedAt = time.Now()
	return m, nil
}

// cycleField advances a cycle-picker row (one with options) to its next
// value, wrapping at the end, and persists immediately. This is the
// Enter behavior for fixed-enum fields like agents.default, where
// tabbing through the choices beats making the user type the exact
// string. The current value is matched against options via get(); an
// unrecognized current value (e.g. a hand-edited config) starts the
// cycle from the first option.
func (m settingsModel) cycleField(f editableField) (settingsModel, tea.Cmd) {
	cur := f.get(&m.cfg)
	next := f.options[0]
	for i, o := range f.options {
		if o == cur {
			next = f.options[(i+1)%len(f.options)]
			break
		}
	}
	if err := f.set(&m.cfg, next); err != nil {
		m.errMsg = err.Error()
		return m, nil
	}
	if err := config.Save(m.cfg); err != nil {
		m.errMsg = "save: " + err.Error()
		return m, nil
	}
	m.errMsg = ""
	m.saveMsg = "saved ✓  " + f.label + " → " + next
	m.savedAt = time.Now()
	return m, nil
}

// openEditor suspends the TUI, opens $EDITOR pointing at config.toml,
// reloads on return. Used for the prose-heavy fields (initial_prompt,
// gitignore_body) that are awkward inside a one-line textinput.
func (m settingsModel) openEditor() (settingsModel, tea.Cmd) {
	editor := strings.TrimSpace(m.cfg.Editor)
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	cfgPath, err := config.Path()
	if err != nil {
		m.errMsg = err.Error()
		return m, nil
	}
	return m, func() tea.Msg {
		return openEditorMsg{Editor: editor, Path: cfgPath, Source: "settings"}
	}
}

// SetConfig replaces the displayed config with the on-disk state.
// Called by the root model after openEditor returns so the screen
// reflects what the user just edited.
func (m *settingsModel) SetConfig(cfg config.Config) {
	m.cfg = cfg
}

func (m settingsModel) View(width, height int) string {
	narrow := isNarrow(width)

	lines := []string{
		m.st.Emphasis.Render("Settings"),
		"",
		m.renderMoshiBlock(),
		"",
	}
	// The version + config-path lines are T2 reference detail, dropped
	// on narrow. The "Editable" header keeps a short section label but
	// sheds the (↑/↓ to move…) instructions (also T2).
	if !narrow {
		cfgPath, _ := config.Path()
		lines = append(lines,
			fmt.Sprintf("ccmux version    %s", m.version),
			fmt.Sprintf("config file      %s", cfgPath),
			"",
			m.st.Subtitle.Render("Editable (↑/↓ to move, enter to edit, e to open config in $EDITOR)"),
		)
	} else {
		lines = append(lines, m.st.Subtitle.Render("Editable"))
	}

	fields := editableFields()
	for i, f := range fields {
		val := f.get(&m.cfg)
		display := val
		if display == "" {
			display = m.st.Muted.Render("(default)")
		}
		cursor := "  "
		if i == m.cursor {
			cursor = m.st.Key.Render("▸ ")
		}
		row := fmt.Sprintf("%s%-22s %s", cursor, f.label, display)
		if f.readOnly {
			row += m.st.Muted.Render("  (read-only)")
		}
		lines = append(lines, row)
		if i == m.cursor {
			lines = append(lines, "  "+m.st.Muted.Render(f.hint))
			if m.editing {
				lines = append(lines, "  "+m.editor.View())
				lines = append(lines, "  "+m.st.Muted.Render("enter to save, esc to cancel"))
				if m.errMsg != "" {
					lines = append(lines, "  "+m.st.StatusError.Render("✗ "+m.errMsg))
				}
			} else if m.errMsg != "" {
				lines = append(lines, "  "+m.st.StatusError.Render("✗ "+m.errMsg))
			}
		}
	}

	if m.saveMsg != "" && time.Since(m.savedAt) < 3*time.Second {
		lines = append(lines, "")
		lines = append(lines, m.st.StatusGood.Render(m.saveMsg))
	}

	lines = append(lines,
		"",
		m.st.Subtitle.Render("Sleep prevention"),
		fmt.Sprintf("  mode           %s", sleepModeDisplay(m.cfg.Sleep)),
		fmt.Sprintf("  idle release   %d minutes", m.cfg.Sleep.IdleReleaseMinutes),
		fmt.Sprintf("  low-batt cutoff %d%% (dangerous auto-downgrades below this)", m.cfg.Sleep.LowBatteryCutoff),
		"",
		m.st.Subtitle.Render("Daemon"),
		fmt.Sprintf("  poll interval  %ds", m.cfg.Daemon.PollIntervalSeconds),
		fmt.Sprintf("  needs-input idle %ds", m.cfg.Daemon.IdleSecondsForNeedsInput),
		fmt.Sprintf("  tailnet listen %v (port %d)", m.cfg.Daemon.ListenTailnet, m.cfg.Daemon.TailnetPort),
		"",
		m.st.Subtitle.Render("Remote hosts"),
		m.renderHosts(),
	)
	return m.st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(strings.Join(lines, "\n"))
}

// renderMoshiBlock shows the most useful one-glance view of the mobile
// push pipeline state. The exact wording follows the steps in
// `ccmux moshi-setup`, so users know what to do next.
func (m settingsModel) renderMoshiBlock() string {
	s := m.moshiState
	title := m.st.Subtitle.Render("Moshi (mobile push)")
	var blockLines []string
	switch {
	case !s.BinaryInstalled:
		blockLines = []string{
			m.st.Muted.Render("  · moshi-hook not installed."),
			"  Run " + m.st.Key.Render("ccmux moshi-setup") + " in a shell to install + pair.",
		}
	case !s.Paired:
		blockLines = []string{
			m.st.StatusWarning.Render("  · moshi-hook installed but not paired."),
			"  Run " + m.st.Key.Render("ccmux moshi-setup") + " and provide a token from the Moshi app.",
		}
	case !s.HooksInstalled:
		blockLines = []string{
			m.st.StatusWarning.Render("  ⚠ paired but Claude Code hooks not wired."),
			"  Run " + m.st.Key.Render("moshi-hook install"),
		}
	case !s.ServiceRunning:
		blockLines = []string{
			m.st.StatusWarning.Render("  ⚠ hooks wired but daemon not running."),
			"  Run " + m.st.Key.Render("brew services start moshi-hook"),
		}
	default:
		blockLines = []string{
			m.st.StatusGood.Render("  ✓ installed, paired, hooks wired, service running."),
			m.st.Muted.Render("  ccmuxd will defer to moshi-hook for push notifications."),
		}
	}
	return strings.Join(append([]string{title}, blockLines...), "\n")
}

func (m settingsModel) renderHosts() string {
	if len(m.cfg.Hosts) == 0 {
		return m.st.Muted.Render("  (none pinned — tailnet peers running ccmuxd are auto-discovered.\n   Use `ccmux host add` only for non-Tailscale hosts or non-default ports.)")
	}
	out := []string{}
	for _, h := range m.cfg.Hosts {
		out = append(out, fmt.Sprintf("  %s  %s@%s  mosh=%v", m.st.HostColor(h.Name).Render("●"), h.User, h.Address, h.Mosh))
	}
	return strings.Join(out, "\n")
}

// sleepModeDisplay resolves the effective mode for the settings panel:
// honors the explicit `mode` if set, otherwise falls back to the legacy
// `dangerous_keep_awake_on_battery` flag so old configs still display
// what they actually do.
func sleepModeDisplay(s config.SleepConfig) string {
	if s.Mode != "" {
		return s.Mode
	}
	if s.DangerousKeepAwakeOnBattery {
		return "dangerous (legacy flag)"
	}
	return "safe"
}
