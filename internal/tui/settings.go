package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/tui/components"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// settingsModel surfaces ccmux's own config.toml with toggles and links,
// plus a Moshi/moshi-hook status block at the top so users can see at a
// glance whether the mobile push pipeline is set up.
//
// Editing model (added in v0.1.x): a cursor moves over the editable
// fields (projects.root, subscription.tier, agents.default). Enter
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
	moshiProbe spinner.Model // animates while the Moshi probe is in flight

	cursor  int // index into editableFields()
	editing bool
	editor  textinput.Model
	errMsg  string
	saveMsg string // transient "saved ✓" message
	savedAt time.Time
	lastErr string // last save-failure error, surfaced in the info modal

	// cfgPath overrides the user's actual config.Path() result.
	// Production leaves this empty and View falls back to
	// config.Path(); golden tests set it to a stable string so the
	// snapshot doesn't drift across machines.
	cfgPath string
}

// SetCfgPath overrides the path rendered in the wide-mode
// "config file" row. Used only by golden tests; the production
// model leaves cfgPath empty and resolves config.Path() at render.
func (m *settingsModel) SetCfgPath(p string) { m.cfgPath = p }

// editableField is one row the user can move the cursor onto. The
// get/set closures let us model plain strings (projects.root), enum
// cycle-pickers (agents.default), and read-only rows uniformly.
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

	// chip marks fields whose value is a fixed enum or boolean and
	// should render as a [value] chip rather than free text. Off-row
	// chips render muted; the active-row chip uses Semantic.Accent.
	chip bool
}

// fieldGroup labels a contiguous slice of editable fields rendered as
// one Subtitle-headed sub-section. Groups carry no state; they're a
// pure View concern derived from editableFields() in groupedFields().
type fieldGroup struct {
	label  string
	fields []editableField
}

func editableFields() []editableField {
	return []editableField{
		{
			label: "subscription.tier",
			hint:  "Drives the dashboard quota bar. One of: api, pro, max5x, max20x.",
			chip:  true,
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
			label:   "agents.default",
			hint:    "Default agent for new projects and bare sessions. Enter cycles: claude → codex → antigravity → cursor → shell.",
			options: []string{"claude", "codex", "antigravity", "cursor", "shell"},
			chip:    true,
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
					return fmt.Errorf("must be one of: claude, codex, antigravity, cursor, shell")
				}
				c.Agents.Default = string(id)
				return nil
			},
		},
		{
			label: "sessions.attach_mode",
			hint:  "mirror = other devices stay attached (default); exclusive = attaching detaches them.",
			chip:  true,
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
			chip:  true,
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

// groupedFields returns the editable-field set partitioned into the
// three Subtitle-headed sub-sections required by the design-system
// spec: Subscription, Projects, Agents. The cursor index into the
// flat editableFields() list is preserved by walking the groups in
// the same order. Fields without a dedicated section (sessions,
// updates, theme) fall under Agents — that group reads as
// "agent and session runtime behavior" rather than just the agent
// picker.
func groupedFields(fields []editableField) []fieldGroup {
	groups := []fieldGroup{
		{label: "Subscription"},
		{label: "Projects"},
		{label: "Agents"},
	}
	for _, f := range fields {
		switch f.label {
		case "subscription.tier":
			groups[0].fields = append(groups[0].fields, f)
		case "projects.root":
			groups[1].fields = append(groups[1].fields, f)
		default:
			groups[2].fields = append(groups[2].fields, f)
		}
	}
	return groups
}

func newSettings(st styles.Styles, km Keymap, cfg config.Config, version string) settingsModel {
	// moshi/moshi-hook status is detected asynchronously — App fires
	// detectMoshiCmd at startup and again every 30s while this screen is
	// focused, delivering the result via SetMoshiState. Detecting it here
	// would shell out (launchctl/brew) and stall the first frame by up
	// to 2s on every launch.
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(st.Semantic.Accent)
	return settingsModel{st: st, km: km, cfg: cfg, version: version, moshiProbe: sp}
}

// SetMoshiState records the result of an async moshi probe (see
// detectMoshiCmd) and resets the staleness clock.
func (m *settingsModel) SetMoshiState(s moshi.Status) {
	m.moshiState = s
	m.moshiCheck = time.Now()
}

// MoshiStale reports whether the cached moshi status is older than 30s
// and due for a refresh. App polls this while the Settings screen is
// focused and fires detectMoshiCmd when it returns true. The zero
// moshiCheck (fresh model, never probed) reads as stale, so the first
// poll always detects.
func (m settingsModel) MoshiStale() bool {
	return time.Since(m.moshiCheck) > 30*time.Second
}

// moshiProbing reports whether we're still waiting on the first Moshi
// probe to come back. While true, the View renders the spinner instead
// of the not-yet-installed placeholder so the user sees motion rather
// than an inaccurate "moshi-hook not installed" verdict.
func (m settingsModel) moshiProbing() bool {
	return m.moshiCheck.IsZero()
}

// SpinnerTick returns the command that drives the moshi-probe spinner
// while it's animating. The App batches this into the screen-focus
// switch so the spinner only ticks when the Settings screen is visible.
func (m settingsModel) SpinnerTick() tea.Cmd {
	return m.moshiProbe.Tick
}

func (m settingsModel) Update(msg tea.Msg) (settingsModel, tea.Cmd) {
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

	// Forward spinner ticks regardless of editing state so the Moshi
	// probe's animation keeps advancing in the background.
	if _, ok := msg.(spinner.TickMsg); ok {
		var cmd tea.Cmd
		m.moshiProbe, cmd = m.moshiProbe.Update(msg)
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
		m.lastErr = err.Error()
		return m, nil
	}
	m.editing = false
	m.errMsg = ""
	m.saveMsg = "saved ✓"
	m.savedAt = time.Now()
	m.lastErr = ""
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
		m.lastErr = err.Error()
		return m, nil
	}
	m.errMsg = ""
	m.saveMsg = "saved ✓  " + f.label + " → " + next
	m.savedAt = time.Now()
	m.lastErr = ""
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

// HelpBarProps returns the screen-specific key hints for Settings.
// `r` (refresh) is intentionally omitted — Settings has no remote
// state to refetch; the hint used to be a silent no-op.
func (m settingsModel) HelpBarProps(width int) components.HelpBarProps {
	hints := []components.KeyHint{
		{Key: "?", Label: "help", Priority: 10},
		{Key: "q", Label: "quit", Priority: 10},
		{Key: "i", Label: "info", Priority: 8},
		{Key: "e", Label: "edit config", Priority: 7},
		{Key: "1-7", Label: "screens", Priority: 2},
	}
	if m.editing {
		hints = append(hints,
			components.KeyHint{Key: "enter", Label: "save", Priority: 9},
			components.KeyHint{Key: "esc", Label: "cancel", Priority: 9},
		)
	}
	return components.HelpBarProps{Hints: hints, Width: width}
}

func (m settingsModel) View(width, height int) string {
	paneInner := width - 2 - 2*m.st.Spacing.SM

	lines := []string{
		m.st.Emphasis.Render("Settings"),
		"",
		m.renderMoshiBlock(),
		"",
	}

	fields := editableFields()
	groups := groupedFields(fields)
	// Walk the flat field list in editableFields() order so the group
	// renderer can map a field back to its global cursor index.
	indexByLabel := make(map[string]int, len(fields))
	for i, f := range fields {
		indexByLabel[f.label] = i
	}

	for gi, g := range groups {
		if len(g.fields) == 0 {
			continue
		}
		if gi > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, m.st.Subtitle.Render(g.label))
		for _, f := range g.fields {
			idx := indexByLabel[f.label]
			lines = append(lines, m.renderFieldRow(f, idx == m.cursor, paneInner))
			if idx == m.cursor {
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
	}

	if m.saveMsg != "" && time.Since(m.savedAt) < 3*time.Second {
		lines = append(lines, "")
		lines = append(lines, m.st.StatusGood.Render(m.saveMsg))
	}

	lines = append(lines,
		"",
		m.st.Subtitle.Render("Sleep prevention"),
		"  "+fmt.Sprintf("mode             %s", m.renderChip(sleepModeDisplay(m.cfg.Sleep), false)),
		"  "+fmt.Sprintf("idle release     %d minutes", m.cfg.Sleep.IdleReleaseMinutes),
		"  "+fmt.Sprintf("low-batt cutoff  %d%%", m.cfg.Sleep.LowBatteryCutoff),
		"  "+m.st.Muted.Render("dangerous mode auto-downgrades below the cutoff"),
		"",
		m.st.Subtitle.Render("Daemon"),
		"  "+fmt.Sprintf("poll interval    %ds", m.cfg.Daemon.PollIntervalSeconds),
		"  "+fmt.Sprintf("needs-input idle %ds", m.cfg.Daemon.IdleSecondsForNeedsInput),
		"  "+fmt.Sprintf("tailnet listen   %s (port %d)", m.renderChip(boolOnOff(m.cfg.Daemon.ListenTailnet), false), m.cfg.Daemon.TailnetPort),
		"",
		m.st.Subtitle.Render("Hosts"),
		m.renderHosts(),
	)
	return m.st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(strings.Join(lines, "\n"))
}

// renderFieldRow lays out a single editable field as a design-system
// list row: 2-cell prefix (selection bar on the active row, two spaces
// otherwise), label, value (or chip), optional read-only tag.
func (m settingsModel) renderFieldRow(f editableField, active bool, paneInner int) string {
	rawVal := f.get(&m.cfg)
	var valStr string
	switch {
	case rawVal == "":
		valStr = m.st.Muted.Render("(default)")
	case f.chip:
		valStr = m.renderChip(rawVal, active)
	case looksLikePath(rawVal):
		// Collapse $HOME to "~/" so sandbox /tmp/... paths don't
		// leak into demo GIFs and the user sees the short form
		// they typed (e.g. "~/Projects") rather than the absolute
		// resolution.
		valStr = summarizePath(rawVal)
	default:
		valStr = rawVal
	}
	content := fmt.Sprintf("%-22s %s", f.label, valStr)
	if f.readOnly {
		content += "  " + m.st.Muted.Render("(read-only)")
	}
	return components.RenderListRow(m.st, content, active, paneInner)
}

// renderChip renders a value as a bracketed chip. The active row's
// chip uses Semantic.Accent so it pops against the elevated background;
// off-row chips render muted to match the surrounding row text.
func (m settingsModel) renderChip(value string, active bool) string {
	style := m.st.Muted
	if active {
		style = lipgloss.NewStyle().Foreground(m.st.Semantic.Accent).Bold(true)
	}
	return style.Render("[" + value + "]")
}

// boolOnOff renders a Go bool as the project's "on"/"off" idiom so
// boolean rows can flow through the same chip-rendering path as enums.
func boolOnOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// renderMoshiBlock shows the most useful one-glance view of the mobile
// push pipeline state. The exact wording follows the steps in
// `ccmux moshi-setup`, so users know what to do next.
func (m settingsModel) renderMoshiBlock() string {
	s := m.moshiState
	title := m.st.Subtitle.Render("Moshi (mobile push)")
	var blockLines []string
	if m.moshiProbing() {
		blockLines = []string{
			"  " + m.moshiProbe.View() + " " + m.st.Muted.Render("detecting moshi-hook…"),
		}
		return strings.Join(append([]string{title}, blockLines...), "\n")
	}
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

// renderSettingsInfoOverlay produces the centered "i" info modal: ccmux
// version, config + log paths, last-save status, and a hint to dismiss.
// Lives next to settingsModel so the modal renders straight off the
// model's own state without needing an extra struct.
func (m settingsModel) renderSettingsInfoOverlay(width, height int) string {
	st := m.st
	cfgPath := m.cfgPath
	if cfgPath == "" {
		cfgPath, _ = config.Path()
	}
	logPath := ccmuxLogPath()

	lines := []string{
		st.Emphasis.Render("ccmux info"),
		st.Subtitle.Render("Reference metadata: version, paths, last save."),
		"",
		fmt.Sprintf("  %s   %s", st.Key.Render("version "), m.version),
		fmt.Sprintf("  %s   %s", st.Key.Render("config  "), summarizePath(cfgPath)),
		fmt.Sprintf("  %s   %s", st.Key.Render("log     "), summarizePath(logPath)),
		"",
	}
	if !m.savedAt.IsZero() {
		lines = append(lines, fmt.Sprintf("  %s   %s ago",
			st.Key.Render("saved   "),
			humanDuration(time.Since(m.savedAt))))
	} else {
		lines = append(lines, "  "+st.Muted.Render("no saves this session"))
	}
	if m.lastErr != "" {
		lines = append(lines, "  "+st.StatusError.Render("last error: "+m.lastErr))
	}

	lines = append(lines, "", st.Muted.Render("press i or esc to close"))

	modalW := minInt(96, width-4)
	body := strings.Join(lines, "\n")
	modal := st.PaneFocused.Width(modalW).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// ccmuxLogPath returns the well-known TUI debug log path. Centralized
// here so the info modal doesn't have to know the layout debug.go uses.
func ccmuxLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.local/state/ccmux/ccmux.log"
	}
	return home + "/.local/state/ccmux/ccmux.log"
}
