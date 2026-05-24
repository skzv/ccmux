package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/claudeconfig"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// claudeModel is the Claude Code config screen — model picker on top,
// then hooks / MCP servers / permissions / commands / skills blocks.
// Reads ~/.claude/settings.json via internal/claudeconfig and supports
// a minimal subset of edits (model + reasoning effort + always-thinking
// toggle through pickers / a keystroke); deeper edits open the
// underlying file in $EDITOR.
type claudeModel struct {
	st             styles.Styles
	km             Keymap
	settings       *claudeconfig.Settings
	commands       []claudeconfig.Command
	skills         []claudeconfig.Skill
	model          string
	modelSource    string
	effort         string
	effortSource   string
	alwaysThinking bool
	yolo           bool
	yoloSource     string
	paths          claudeconfig.Locations
	lastBackup     string
	picker         pickerKind
	pickerCursor   int
	editor         string
	narrow         bool // terminal is below the layout breakpoint
}

// pickerKind identifies which modal picker is currently open on the
// Claude screen. pickerNone means no modal is showing.
type pickerKind int

const (
	pickerNone pickerKind = iota
	pickerModel
	pickerEffort
)

func newClaude(st styles.Styles, km Keymap) claudeModel {
	m := claudeModel{st: st, km: km, editor: pickEditor()}
	m.reload()
	return m
}

func (m *claudeModel) reload() {
	if p, err := claudeconfig.Paths(); err == nil {
		m.paths = p
	}
	if s, err := claudeconfig.ReadSettings(); err == nil {
		m.settings = s
	}
	m.commands, _ = claudeconfig.ListCommands()
	m.skills, _ = claudeconfig.ListSkills()
	m.model, m.modelSource = claudeconfig.EffectiveModel()
	m.effort, m.effortSource = claudeconfig.EffectiveEffortLevel()
	m.yolo, m.yoloSource = claudeconfig.EffectiveYoloMode()
	if m.settings != nil {
		m.alwaysThinking = m.settings.AlwaysThinkingEnabled
	}
}

func (m claudeModel) Update(msg tea.Msg) (claudeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case claudeReloadMsg:
		m.reload()
		return m, nil

	case claudeModelChangedMsg:
		if msg.Err != nil {
			return m, toastCmd(toastError, "model: "+msg.Err.Error(), 5)
		}
		m.lastBackup = msg.Backup
		m.reload()
		display := msg.New
		if display == "" {
			display = "(no override)"
		}
		return m, toastCmd(toastSuccess,
			fmt.Sprintf("model set to %s — backup at %s", display, summarizePath(msg.Backup)),
			6,
		)

	case claudeEffortChangedMsg:
		if msg.Err != nil {
			return m, toastCmd(toastError, "effort: "+msg.Err.Error(), 5)
		}
		m.lastBackup = msg.Backup
		m.reload()
		display := msg.New
		if display == "" {
			display = "(no override)"
		}
		return m, toastCmd(toastSuccess,
			fmt.Sprintf("reasoning effort set to %s — backup at %s", display, summarizePath(msg.Backup)),
			6,
		)

	case claudeAlwaysThinkingChangedMsg:
		if msg.Err != nil {
			return m, toastCmd(toastError, "always-thinking: "+msg.Err.Error(), 5)
		}
		m.lastBackup = msg.Backup
		m.reload()
		label := "off"
		if msg.New {
			label = "on"
		}
		return m, toastCmd(toastSuccess,
			fmt.Sprintf("always-thinking turned %s — backup at %s", label, summarizePath(msg.Backup)),
			6,
		)

	case claudeYoloChangedMsg:
		if msg.Err != nil {
			return m, toastCmd(toastError, "yolo: "+msg.Err.Error(), 5)
		}
		m.lastBackup = msg.Backup
		m.reload()
		label := "off"
		if msg.New {
			label = "on"
		}
		return m, toastCmd(toastSuccess,
			fmt.Sprintf("yolo mode turned %s — backup at %s", label, summarizePath(msg.Backup)),
			6,
		)

	case tea.KeyMsg:
		if m.picker != pickerNone {
			return m.updatePicker(msg)
		}
		switch msg.String() {
		case "m":
			m.picker = pickerModel
			m.pickerCursor = 0
			cur := strings.ToLower(strings.TrimSpace(m.settings.Model))
			for i, opt := range claudeconfig.KnownModels() {
				if opt.Alias == cur {
					m.pickerCursor = i
					break
				}
			}
		case "e":
			m.picker = pickerEffort
			m.pickerCursor = 0
			cur := strings.ToLower(strings.TrimSpace(m.settings.EffortLevel))
			for i, opt := range claudeconfig.KnownEffortLevels() {
				if opt.Value == cur {
					m.pickerCursor = i
					break
				}
			}
		case "a":
			toggled := !m.alwaysThinking
			return m, func() tea.Msg {
				backup, err := claudeconfig.SetAlwaysThinking(toggled)
				return claudeAlwaysThinkingChangedMsg{New: toggled, Backup: backup, Err: err}
			}
		case "y":
			toggled := !m.yolo
			return m, func() tea.Msg {
				backup, err := claudeconfig.SetYoloMode(toggled)
				return claudeYoloChangedMsg{New: toggled, Backup: backup, Err: err}
			}
		case "c":
			return m, openClaudeFileCmd(m.editor, m.paths.GlobalCLAUDEMd, true)
		case "j":
			return m, openClaudeFileCmd(m.editor, m.paths.Settings, false)
		}
	}
	return m, nil
}

func (m claudeModel) updatePicker(msg tea.KeyMsg) (claudeModel, tea.Cmd) {
	var optsLen int
	switch m.picker {
	case pickerModel:
		optsLen = len(claudeconfig.KnownModels())
	case pickerEffort:
		optsLen = len(claudeconfig.KnownEffortLevels())
	}
	switch msg.String() {
	case "esc":
		m.picker = pickerNone
	case "up", "k":
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case "down", "j":
		if m.pickerCursor < optsLen-1 {
			m.pickerCursor++
		}
	case "enter":
		cursor := m.pickerCursor
		which := m.picker
		m.picker = pickerNone
		switch which {
		case pickerModel:
			chosen := claudeconfig.KnownModels()[cursor].Alias
			return m, func() tea.Msg {
				backup, err := claudeconfig.SetModel(chosen)
				return claudeModelChangedMsg{New: chosen, Backup: backup, Err: err}
			}
		case pickerEffort:
			chosen := claudeconfig.KnownEffortLevels()[cursor].Value
			return m, func() tea.Msg {
				backup, err := claudeconfig.SetEffortLevel(chosen)
				return claudeEffortChangedMsg{New: chosen, Backup: backup, Err: err}
			}
		}
	}
	return m, nil
}

func (m claudeModel) View(width, height int) string {
	m.narrow = isNarrow(width) // m is a value copy; the mutation stays local
	if m.picker != pickerNone {
		return m.viewPicker(width, height)
	}
	return m.viewMain(width, height)
}

func (m claudeModel) viewMain(width, height int) string {
	st := m.st
	lines := []string{st.Emphasis.Render("Claude Code Configuration")}
	// The settings-file path is T2 — drop it on narrow.
	if !m.narrow {
		lines = append(lines, st.Muted.Render("settings: "+m.paths.Settings))
	}
	lines = append(lines,
		"",
		m.renderModelBlock(),
		"",
		m.renderEffortBlock(),
		"",
		m.renderSafetyBlock(),
		"",
		m.renderHooksBlock(),
		"",
		m.renderMCPBlock(),
		"",
		m.renderPermissionsBlock(),
		"",
		m.renderCommandsBlock(),
		"",
		m.renderSkillsBlock(),
	)
	// The Keys cheatsheet and the backup-path note are T2.
	if !m.narrow {
		lines = append(lines,
			"",
			st.Subtitle.Render("Keys"),
			"  "+st.Key.Render("m")+"  pick default model       "+st.Key.Render("e")+"  pick reasoning effort",
			"  "+st.Key.Render("a")+"  toggle always-thinking    "+st.Key.Render("y")+"  toggle yolo mode",
			"  "+st.Key.Render("c")+"  edit global CLAUDE.md     "+st.Key.Render("j")+"  edit settings.json",
			"  "+st.Key.Render("5")+"  open project notes",
		)
		if m.lastBackup != "" {
			lines = append(lines, "", st.Muted.Render("last write backed up to "+summarizePath(m.lastBackup)))
		}
	}
	return st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(strings.Join(lines, "\n"))
}

func (m claudeModel) renderModelBlock() string {
	st := m.st
	hint := "(set in " + m.modelSource + ")"
	lines := []string{
		st.Subtitle.Render("Default model"),
		"  " + st.Emphasis.Render(m.model) + "  " + st.Muted.Render(hint),
	}
	if !m.narrow {
		lines = append(lines, st.Muted.Render("  press "+st.Key.Render("m")+" to change"))
	}
	return strings.Join(lines, "\n")
}

func (m claudeModel) renderEffortBlock() string {
	st := m.st
	hint := "(set in " + m.effortSource + ")"
	thinkLabel := "off"
	if m.alwaysThinking {
		thinkLabel = "on"
	}
	lines := []string{
		st.Subtitle.Render("Reasoning effort"),
		"  " + st.Emphasis.Render(m.effort) + "  " + st.Muted.Render(hint),
		"  always-thinking: " + st.Emphasis.Render(thinkLabel),
	}
	if !m.narrow {
		lines = append(lines,
			st.Muted.Render("  "+st.Key.Render("e")+" pick effort  · "+st.Key.Render("a")+" toggle always-thinking"),
			st.Muted.Render("  (CLI override: `claude --effort <low|medium|high|xhigh|max>` per session)"),
		)
	}
	return strings.Join(lines, "\n")
}

// renderSafetyBlock surfaces the YOLO toggle. Pulled into its own block
// rather than tacked onto the effort one so users can't miss the
// safety-relevant state at a glance.
func (m claudeModel) renderSafetyBlock() string {
	st := m.st
	yoloLabel := "off"
	if m.yolo {
		yoloLabel = "on"
	}
	hint := "(set in " + m.yoloSource + ")"
	lines := []string{
		st.Subtitle.Render("Safety"),
		"  yolo mode: " + st.Emphasis.Render(yoloLabel) + "  " + st.Muted.Render(hint),
	}
	if !m.narrow {
		lines = append(lines,
			st.Muted.Render("  "+st.Key.Render("y")+" toggle  · writes permissions.defaultMode = \"bypassPermissions\""),
			st.Muted.Render("  (CLI override: `claude --dangerously-skip-permissions` per session)"),
		)
	}
	return strings.Join(lines, "\n")
}

func (m claudeModel) renderHooksBlock() string {
	st := m.st
	if m.settings == nil || len(m.settings.Hooks) == 0 {
		return st.Subtitle.Render("Hooks") + "\n  " + st.Muted.Render("(none)")
	}
	out := []string{st.Subtitle.Render("Hooks")}
	preferredOrder := []string{"SessionStart", "UserPromptSubmit", "PermissionRequest", "Stop"}
	ordered := []string{}
	seen := map[string]bool{}
	for _, p := range preferredOrder {
		if _, ok := m.settings.Hooks[p]; ok {
			ordered = append(ordered, p)
			seen[p] = true
		}
	}
	for k := range m.settings.Hooks {
		if !seen[k] {
			ordered = append(ordered, k)
		}
	}
	for _, lc := range ordered {
		groups := m.settings.Hooks[lc]
		count := 0
		var first string
		for _, g := range groups {
			for _, h := range g.Hooks {
				count++
				if first == "" {
					first = h.Command
					if len(first) > 60 {
						first = first[:57] + "…"
					}
				}
			}
		}
		out = append(out, fmt.Sprintf("  %-22s %d hook(s)  %s",
			lc, count, st.Muted.Render(first)))
	}
	return strings.Join(out, "\n")
}

func (m claudeModel) renderMCPBlock() string {
	st := m.st
	if m.settings == nil || len(m.settings.MCPServers) == 0 {
		return st.Subtitle.Render("MCP servers") + "\n  " + st.Muted.Render("(none configured)")
	}
	out := []string{st.Subtitle.Render(fmt.Sprintf("MCP servers (%d)", len(m.settings.MCPServers)))}
	names := make([]string, 0, len(m.settings.MCPServers))
	for k := range m.settings.MCPServers {
		names = append(names, k)
	}
	cap := 5
	for i, n := range names {
		if i >= cap {
			out = append(out, st.Muted.Render(fmt.Sprintf("  … and %d more", len(names)-cap)))
			break
		}
		s := m.settings.MCPServers[n]
		kind := s.Type
		if kind == "" {
			if s.URL != "" {
				kind = "http"
			} else if s.Command != "" {
				kind = "stdio"
			}
		}
		out = append(out, fmt.Sprintf("  %-22s %s", n, st.Muted.Render(kind)))
	}
	return strings.Join(out, "\n")
}

func (m claudeModel) renderPermissionsBlock() string {
	st := m.st
	if m.settings == nil || (len(m.settings.Permissions.Allow) == 0 && len(m.settings.Permissions.Deny) == 0) {
		return st.Subtitle.Render("Permissions") + "\n  " + st.Muted.Render("(no explicit allow/deny — Claude prompts each time)")
	}
	lines := []string{
		st.Subtitle.Render("Permissions"),
		fmt.Sprintf("  allow: %d pattern(s)", len(m.settings.Permissions.Allow)),
		fmt.Sprintf("  deny:  %d pattern(s)", len(m.settings.Permissions.Deny)),
	}
	if !m.narrow {
		lines = append(lines, st.Muted.Render("  edit with "+st.Key.Render("j")+" (opens settings.json)"))
	}
	return strings.Join(lines, "\n")
}

func (m claudeModel) renderCommandsBlock() string {
	st := m.st
	if len(m.commands) == 0 {
		return st.Subtitle.Render("Slash commands") + "\n  " + st.Muted.Render("(none under "+m.paths.CommandsDir+")")
	}
	out := []string{st.Subtitle.Render(fmt.Sprintf("Slash commands (%d)", len(m.commands)))}
	cap := 5
	for i, c := range m.commands {
		if i >= cap {
			out = append(out, st.Muted.Render(fmt.Sprintf("  … and %d more", len(m.commands)-cap)))
			break
		}
		desc := c.Description
		if desc == "" {
			desc = "—"
		}
		out = append(out, fmt.Sprintf("  /%s   %s", c.Name, st.Muted.Render(desc)))
	}
	return strings.Join(out, "\n")
}

func (m claudeModel) renderSkillsBlock() string {
	st := m.st
	if len(m.skills) == 0 {
		return st.Subtitle.Render("Skills") + "\n  " + st.Muted.Render("(none)")
	}
	out := []string{st.Subtitle.Render(fmt.Sprintf("Skills (%d)", len(m.skills)))}
	cap := 5
	for i, s := range m.skills {
		if i >= cap {
			out = append(out, st.Muted.Render(fmt.Sprintf("  … and %d more", len(m.skills)-cap)))
			break
		}
		desc := s.Description
		if desc == "" {
			desc = "—"
		}
		out = append(out, fmt.Sprintf("  %s   %s", s.Name, st.Muted.Render(desc)))
	}
	return strings.Join(out, "\n")
}

func (m claudeModel) viewPicker(width, height int) string {
	st := m.st
	var title string
	var rows []pickerRow
	switch m.picker {
	case pickerModel:
		title = "Pick default model"
		for _, o := range claudeconfig.KnownModels() {
			rows = append(rows, pickerRow{Label: o.Label, Desc: o.Desc})
		}
	case pickerEffort:
		title = "Pick reasoning effort"
		for _, o := range claudeconfig.KnownEffortLevels() {
			rows = append(rows, pickerRow{Label: o.Label, Desc: o.Desc})
		}
	}
	lines := []string{
		st.Emphasis.Render(title),
		st.Subtitle.Render("Writes to " + m.paths.Settings + " (backed up first)."),
		"",
	}
	for i, o := range rows {
		row := fmt.Sprintf("  %-40s %s", o.Label, st.Muted.Render(o.Desc))
		if i == m.pickerCursor {
			row = st.ListItemSelected.Render(row)
		}
		lines = append(lines, row)
	}
	lines = append(lines, "",
		st.Muted.Render("↑↓ navigate  enter: choose  esc: cancel"),
	)
	modal := st.PaneFocused.Width(minInt(96, width-4)).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// pickerRow is the renderer-side shape a picker option takes: just a
// label and description, decoupled from whether it came from a model
// or effort option.
type pickerRow struct {
	Label string
	Desc  string
}

// openClaudeFileCmd creates the target file if requested, then exec's
// $EDITOR on it. Re-reads the screen state on return.
func openClaudeFileCmd(editor, path string, createIfMissing bool) tea.Cmd {
	if createIfMissing {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			_ = os.WriteFile(path, []byte("# CLAUDE.md\n\n(global Claude Code instructions for this host)\n"), 0o644)
		}
	}
	return openEditorCmd(editor, path, claudeReloadMsg{})
}

// toastCmd is a small helper to emit a transient toast from a screen.
func toastCmd(kind toastKind, text string, ttlSec int) tea.Cmd {
	return func() tea.Msg {
		return toastMsg{Text: text, Kind: kind, Until: time.Now().Add(time.Duration(ttlSec) * time.Second)}
	}
}

// summarizePath replaces the user's home prefix with `~` so long paths
// fit in toasts.
func summarizePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
