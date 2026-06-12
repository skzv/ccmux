package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/claudeconfig"
	"github.com/skzv/ccmux/internal/claudemodels"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/components"
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
	// ccmuxDefaultModel is the per-machine model pin loaded from
	// ~/.config/ccmux/config.toml [claude] default_model. Mirrors the
	// model / effort pattern above (string + a way to display the
	// "(inherit Claude Code)" case). Empty means no pin.
	ccmuxDefaultModel string
	// catalog is the live model list shown by pickerCcmuxModel. Loaded
	// lazily on first open from the daemon's on-disk cache; falls back
	// to the curated in-binary list when the cache file isn't there
	// (fresh install, daemon not running, etc).
	catalog claudemodels.Catalog
	browser agentBrowser
	editor  string
	narrow  bool // terminal is below the layout breakpoint
}

// pickerKind identifies which modal picker is currently open on the
// Claude screen. pickerNone means no modal is showing.
type pickerKind int

const (
	pickerNone pickerKind = iota
	// pickerModel chooses an alias (opus / sonnet / haiku / opusplan) or
	// "inherit". Writes Claude Code's ~/.claude/settings.json so the pick
	// applies to every claude invocation, including ones outside ccmux.
	// Aliases auto-track Anthropic's current bindings (today opus = 4.7,
	// tomorrow whatever they ship next).
	pickerModel
	pickerEffort
	// pickerCcmuxModel pins a specific model for sessions ccmux itself
	// launches. Writes ~/.config/ccmux/config.toml's [claude] section
	// and translates to `ANTHROPIC_MODEL=<id>` at session start. Per-
	// machine; doesn't touch Claude Code's own settings. The list is
	// the daemon's live-discovered catalog (or the curated fallback
	// when the daemon hasn't fetched yet).
	pickerCcmuxModel
)

func newClaude(st styles.Styles, km Keymap) claudeModel {
	m := claudeModel{st: st, km: km, editor: pickEditor(), browser: newAgentBrowser(st)}
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
	if cfg, err := config.Load(); err == nil {
		m.ccmuxDefaultModel = cfg.Claude.DefaultModel
	}
	m.yolo, m.yoloSource = claudeconfig.EffectiveYoloMode()
	if m.settings != nil {
		m.alwaysThinking = m.settings.AlwaysThinkingEnabled
	}
	m.browser.SetSections("Claude Code configured", m.browserSections())
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

	case claudeCcmuxModelChangedMsg:
		if msg.Err != nil {
			return m, toastCmd(toastError, "ccmux model pin: "+msg.Err.Error(), 5)
		}
		m.reload()
		display := msg.New
		if display == "" {
			display = "(no pin — inherit Claude Code)"
		}
		return m, toastCmd(toastSuccess,
			fmt.Sprintf("ccmux default model set to %s — applies on next session launch", display),
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

	case tea.MouseMsg:
		if b, cmd, _ := m.browser.Update(msg); true {
			m.browser = b
			return m, cmd
		}
	case tea.KeyMsg:
		if m.picker != pickerNone {
			return m.updatePicker(msg)
		}
		// Route to the embedded browser first. It consumes navigation
		// (j/k/arrows/enter); any letter key it doesn't bind falls
		// through to the model-/effort-/yolo-picker shortcuts below.
		if b, cmd, handled := m.browser.Update(msg); handled {
			m.browser = b
			return m, cmd
		}
		switch msg.String() {
		case "M":
			m.picker = pickerCcmuxModel
			m.pickerCursor = 0
			// Lazy-load the catalog the first time the user opens
			// this picker. The daemon writes the cache file on a 24h
			// timer; if it doesn't exist yet (fresh install / daemon
			// just started), Read() returns a zero-value Catalog and
			// Service.withFallback would normally fill in. Since the
			// TUI is reading the cache directly, we apply the same
			// merge-with-fallback logic inline.
			if !m.catalogLoaded() {
				m.loadCatalog()
			}
			// Pre-position on the user's current pin so the cursor
			// lands on it.
			for i, mdl := range m.catalogPickerModels() {
				if mdl.ID == m.ccmuxDefaultModel {
					m.pickerCursor = i
					break
				}
			}
		case "m":
			m.picker = pickerModel
			m.pickerCursor = 0
			// Pre-position on the EFFECTIVE model so the cursor lands
			// on whatever Claude Code actually uses right now, not just
			// what's in settings.json. Reading settings.Model misses
			// the case where $ANTHROPIC_MODEL overrides: the picker
			// would open on "Inherit" because settings.Model is empty,
			// the user picks the model they ALREADY use, nothing
			// visibly changes, and they conclude the picker is broken.
			cur := normalizeModelAlias(m.model)
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
	case pickerCcmuxModel:
		optsLen = len(m.catalogPickerModels())
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
		case pickerCcmuxModel:
			rows := m.catalogPickerModels()
			if cursor >= len(rows) {
				return m, nil
			}
			chosen := rows[cursor].ID
			return m, func() tea.Msg {
				err := setCcmuxClaudeDefault(chosen)
				return claudeCcmuxModelChangedMsg{New: chosen, Err: err}
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
	// Fallback wrapper for callers outside agentsModel (e.g. golden
	// tests rendering claudeModel directly). Production renders go
	// through agentsModel.View, which owns the bordered Pane.
	return m.st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(
		m.ViewBody(width-4, height-2))
}

// ViewBody renders the sub-tab's inner content without an outer
// Pane. agentsModel.View wraps every sub-tab in one shared Pane so
// the agent label row + body sit inside one continuous bordered
// block; that contract requires un-wrapped inner content from each
// sub-model.
func (m claudeModel) ViewBody(width, height int) string {
	m.narrow = isNarrow(width)
	st := m.st
	header := []string{
		st.Emphasis.Render("Claude Code Configuration"),
		"",
		st.AgentAccent(agent.IDClaude).Render("Defaults"),
	}
	header = append(header, m.renderDefaultsRows()...)
	header = append(header,
		"",
		st.AgentAccent(agent.IDClaude).Render("Config files"),
	)
	header = append(header, m.renderConfigFilesRows()...)
	header = append(header, "")
	headerStr := strings.Join(header, "\n")
	headerH := lipgloss.Height(headerStr)

	browserH := height - headerH
	if browserH < 8 {
		browserH = 8
	}
	browserView := m.browser.View(width, browserH)
	body := lipgloss.JoinVertical(lipgloss.Left, headerStr, browserView)
	if !m.narrow && m.lastBackup != "" {
		body = lipgloss.JoinVertical(lipgloss.Left, body, st.Muted.Render("last write backed up to "+summarizePath(m.lastBackup)))
	}
	return body
}

// renderDefaultsRows produces the indented label/value/source rows
// under the Defaults header — model, effort, always-thinking, yolo.
// Label column is padded to 18 cells so the value column aligns
// across all four rows; source column is muted parenthetical.
func (m claudeModel) renderDefaultsRows() []string {
	st := m.st
	thinkLabel := "off"
	if m.alwaysThinking {
		thinkLabel = "on"
	}
	yoloLabel := "off"
	if m.yolo {
		yoloLabel = "on"
	}
	row := func(label, value, source string) string {
		out := "  " + fmt.Sprintf("%-18s", label) + st.Emphasis.Render(value)
		if source != "" {
			out += "  " + st.Muted.Render("(from "+source+")")
		}
		return out
	}
	return []string{
		row("model", m.model, m.modelSource),
		row("effort", m.effort, m.effortSource),
		row("always-thinking", thinkLabel, ""),
		row("yolo mode", yoloLabel, m.yoloSource),
	}
}

// renderConfigFilesRows lists the two Claude config-file paths the
// `c` and `j` keys edit. Path column is muted so the file name is the
// visual anchor; HelpBar already advertises c / j as the action keys.
func (m claudeModel) renderConfigFilesRows() []string {
	st := m.st
	row := func(name, path string) string {
		return "  " + fmt.Sprintf("%-18s", name) + st.Muted.Render(summarizePath(path))
	}
	return []string{
		row("CLAUDE.md", m.paths.GlobalCLAUDEMd),
		row("settings.json", m.paths.Settings),
	}
}

// renderConfiguredRows summarizes the five `settings.json` subsystems —
// Hooks, MCP servers, Permissions, Slash commands, Skills — as one row
// each. Each row reads `<label>  <count> <units>  <sample>` so the
// section is scannable at a glance and the file behind it is still
// reachable via `j`.
func (m claudeModel) renderConfiguredRows() []string {
	st := m.st
	row := func(label, value, sample string) string {
		out := "  " + fmt.Sprintf("%-18s", label) + value
		if sample != "" {
			out += "  " + st.Muted.Render(sample)
		}
		return out
	}
	rows := []string{
		row("Hooks", m.hooksSummaryValue(), m.hooksSummarySample()),
		row("MCP servers", m.mcpSummaryValue(), m.mcpSummarySample()),
		row("Permissions", m.permissionsSummaryValue(), ""),
		row("Slash commands", m.commandsSummaryValue(), m.commandsSummarySample()),
		row("Skills", m.skillsSummaryValue(), m.skillsSummarySample()),
	}
	return rows
}

// hooksSummaryValue returns the right-of-label count text for the
// Hooks row ("(none)" when no hooks are configured, otherwise
// "<n> events").
func (m claudeModel) hooksSummaryValue() string {
	if m.settings == nil || len(m.settings.Hooks) == 0 {
		return m.st.Muted.Render("(none)")
	}
	return fmt.Sprintf("%d events", len(m.settings.Hooks))
}

// hooksSummarySample returns the sample event-name preview rendered
// muted to the right of the count. The order is stable across renders:
// preferred Claude lifecycle events (SessionStart, UserPromptSubmit,
// PermissionRequest, Stop) first in that fixed order, then any
// remaining configured events in alphabetical order. Sample is capped
// at 3 names; remainder collapses to ", …" so the row stays one line.
//
// The deterministic order is the fix for the "hooks rows jump around"
// bug — Go's map iteration is intentionally randomized, so the prior
// code surfaced a different right-column preview on every render.
func (m claudeModel) hooksSummarySample() string {
	if m.settings == nil || len(m.settings.Hooks) == 0 {
		return ""
	}
	names := sortedHookEventNames(m.settings.Hooks)
	if len(names) <= 3 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:3], ", ") + ", …"
}

// sortedHookEventNames returns hook event names in a stable display
// order: known Claude lifecycle events first, alphabetical for the
// rest. Lifted out of the renderer so any future caller (a `?` modal,
// for example) renders the same order.
func sortedHookEventNames(hooks map[string][]claudeconfig.HookGroup) []string {
	preferred := []string{"SessionStart", "UserPromptSubmit", "PermissionRequest", "Stop"}
	seen := map[string]bool{}
	out := []string{}
	for _, p := range preferred {
		if _, ok := hooks[p]; ok {
			out = append(out, p)
			seen[p] = true
		}
	}
	rest := []string{}
	for k := range hooks {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func (m claudeModel) mcpSummaryValue() string {
	if m.settings == nil || len(m.settings.MCPServers) == 0 {
		return m.st.Muted.Render("(none)")
	}
	return fmt.Sprintf("%d", len(m.settings.MCPServers))
}

// mcpSummarySample lists the first 3 server names in alphabetical
// order so the row is stable across renders.
func (m claudeModel) mcpSummarySample() string {
	if m.settings == nil || len(m.settings.MCPServers) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.settings.MCPServers))
	for k := range m.settings.MCPServers {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) <= 3 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:3], ", ") + ", …"
}

func (m claudeModel) permissionsSummaryValue() string {
	if m.settings == nil || (len(m.settings.Permissions.Allow) == 0 && len(m.settings.Permissions.Deny) == 0) {
		return m.st.Muted.Render("(prompt each time)")
	}
	return fmt.Sprintf("%d allow · %d deny",
		len(m.settings.Permissions.Allow), len(m.settings.Permissions.Deny))
}

func (m claudeModel) commandsSummaryValue() string {
	if len(m.commands) == 0 {
		return m.st.Muted.Render("(none)")
	}
	return fmt.Sprintf("%d", len(m.commands))
}

func (m claudeModel) commandsSummarySample() string {
	if len(m.commands) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.commands))
	for _, c := range m.commands {
		names = append(names, "/"+c.Name)
	}
	sort.Strings(names)
	if len(names) <= 3 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:3], ", ") + ", …"
}

func (m claudeModel) skillsSummaryValue() string {
	if len(m.skills) == 0 {
		return m.st.Muted.Render("(none)")
	}
	return fmt.Sprintf("%d", len(m.skills))
}

func (m claudeModel) skillsSummarySample() string {
	if len(m.skills) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.skills))
	for _, s := range m.skills {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	if len(names) <= 3 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:3], ", ") + ", …"
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
	case pickerCcmuxModel:
		title = "Pin a model for ccmux-launched sessions"
		for _, mdl := range m.catalogPickerModels() {
			rows = append(rows, pickerRow{
				Label: ccmuxModelLabel(mdl, m.ccmuxDefaultModel),
				Desc:  ccmuxModelDesc(mdl),
			})
		}
	}
	subtitle := "Writes to " + m.paths.Settings + " (backed up first)."
	if m.picker == pickerCcmuxModel {
		// Different surface, different file — make sure the user
		// knows where the pick is going to land.
		subtitle = "Writes [claude] default_model in ~/.config/ccmux/config.toml. " +
			"Applied as ANTHROPIC_MODEL when ccmux launches a Claude session."
	}
	lines := []string{
		st.Emphasis.Render(title),
		st.Subtitle.Render(subtitle),
	}
	// When an environment variable shadows the file value, picking a
	// row here still writes settings.json but the env var keeps
	// winning at the Claude Code layer. Surface that explicitly so
	// the picker doesn't appear broken when nothing visibly changes.
	if m.picker == pickerModel && m.modelSource == "$ANTHROPIC_MODEL" {
		lines = append(lines, st.StatusWarning.Render(
			"⚠ $ANTHROPIC_MODEL="+m.model+" is overriding settings.json. "+
				"Unset it to let your pick take effect.",
		))
	}
	lines = append(lines, "")
	pickerW := minInt(96, width-4) - 2
	for i, o := range rows {
		row := fmt.Sprintf("%-40s %s", o.Label, st.Muted.Render(o.Desc))
		lines = append(lines, components.RenderListRow(st, row, i == m.pickerCursor, pickerW))
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

// normalizeModelAlias maps a value returned by claudeconfig.EffectiveModel
// — which may be a short alias ("opus"), a full ID ("claude-opus-4-7"),
// or the literal "(default)" sentinel — to one of the picker's
// KnownModels aliases so the cursor can pre-position correctly.
// Unknown inputs fall through unchanged so cursor lookup still works
// for aliases the user added directly to settings.json.
func normalizeModelAlias(model string) string {
	s := strings.ToLower(strings.TrimSpace(model))
	if s == "" || s == "(default)" {
		return "" // matches the "Inherit / no override" row
	}
	switch s {
	case "claude-opus-4-7", "claude-opus-4-1", "claude-opus-4":
		return "opus"
	case "claude-sonnet-4-6", "claude-sonnet-4-5", "claude-sonnet-4":
		return "sonnet"
	case "claude-haiku-4-5", "claude-haiku-4":
		return "haiku"
	}
	return s
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

// summarizePath replaces the user's home prefix with `~` so long
// paths fit in toasts and detail panes.
//
// Both arguments are passed through filepath.Clean so a stray double
// slash in $HOME (macOS's $TMPDIR has a trailing /, which leaks into
// derived paths in the VHS demo harness) doesn't prevent the prefix
// match. Same hardening as cmd/ccmux/cmd.tildify.
func summarizePath(p string) string {
	if p == "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	cleanP := filepath.Clean(p)
	cleanHome := filepath.Clean(home)
	if cleanP == cleanHome {
		return "~"
	}
	if strings.HasPrefix(cleanP, cleanHome+string(filepath.Separator)) {
		return "~" + cleanP[len(cleanHome):]
	}
	return p
}

// looksLikePath returns true when `s` is shaped like a filesystem
// path that summarizePath could meaningfully shorten. Used by the
// Settings screen so we only tildify string-valued rows that are
// actually paths — not booleans, enums, or numbers that happen to
// contain a slash by coincidence. Conservative: an absolute path
// (`/...`), an explicit home shortcut (`~/...`), or a unix:// URL.
func looksLikePath(s string) bool {
	if s == "" {
		return false
	}
	return strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "~/") ||
		strings.HasPrefix(s, "unix://")
}

// browserSections builds the agent-browser sections for the Claude
// sub-tab. Sections appear in a fixed order — Hooks, MCP servers,
// Slash commands, Skills — to match the Configured row order and
// keep the browser's vertical layout predictable across renders.
// Items within each section are sorted alphabetically (hooks by
// event lifecycle order). Preview text is plain — no Glamour — so
// the browser's hard-wrap stays accurate.
func (m claudeModel) browserSections() []agentBrowserSection {
	out := []agentBrowserSection{}
	out = append(out, m.browserHooksSection())
	out = append(out, m.browserMCPSection())
	out = append(out, m.browserCommandsSection())
	out = append(out, m.browserSkillsSection())
	return out
}

func (m claudeModel) browserHooksSection() agentBrowserSection {
	section := agentBrowserSection{Title: "Hooks", Color: m.st.P.Peach}
	if m.settings == nil || len(m.settings.Hooks) == 0 {
		return section
	}
	for _, event := range sortedHookEventNames(m.settings.Hooks) {
		groups := m.settings.Hooks[event]
		count := 0
		preview := []string{event, ""}
		for _, g := range groups {
			for _, h := range g.Hooks {
				count++
				preview = append(preview, "  command: "+h.Command)
				if h.Timeout > 0 {
					preview = append(preview, fmt.Sprintf("  timeout: %ds", h.Timeout))
				}
				preview = append(preview, "")
			}
		}
		section.Items = append(section.Items, agentBrowserItem{
			Label:    event,
			Trailing: fmt.Sprintf("%d hook(s)", count),
			Preview:  strings.Join(preview, "\n"),
		})
	}
	return section
}

func (m claudeModel) browserMCPSection() agentBrowserSection {
	section := agentBrowserSection{Title: "MCP servers", Color: m.st.P.Sky}
	if m.settings == nil || len(m.settings.MCPServers) == 0 {
		return section
	}
	names := make([]string, 0, len(m.settings.MCPServers))
	for k := range m.settings.MCPServers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		s := m.settings.MCPServers[name]
		kind := s.Type
		if kind == "" {
			if s.URL != "" {
				kind = "http"
			} else if s.Command != "" {
				kind = "stdio"
			}
		}
		preview := []string{name, "", "  type: " + kind}
		if s.URL != "" {
			preview = append(preview, "  url: "+s.URL)
		}
		if s.Command != "" {
			preview = append(preview, "  command: "+s.Command)
		}
		if len(s.Args) > 0 {
			preview = append(preview, "  args: "+strings.Join(s.Args, " "))
		}
		if len(s.Env) > 0 {
			envKeys := make([]string, 0, len(s.Env))
			for k := range s.Env {
				envKeys = append(envKeys, k)
			}
			sort.Strings(envKeys)
			preview = append(preview, "  env keys: "+strings.Join(envKeys, ", "))
		}
		section.Items = append(section.Items, agentBrowserItem{
			Label:    name,
			Trailing: kind,
			Preview:  strings.Join(preview, "\n"),
		})
	}
	return section
}

func (m claudeModel) browserCommandsSection() agentBrowserSection {
	section := agentBrowserSection{Title: "Commands", Color: m.st.P.Green}
	for _, c := range m.commands {
		// The Command struct only carries Description in-memory; the
		// underlying ~/.claude/commands/<name>.md body lives on disk
		// and we read it lazily so the browser can render the full
		// content (frontmatter + markdown) via Glamour.
		body := readFileOr(c.Path, c.Description)
		section.Items = append(section.Items, agentBrowserItem{
			Label:    "/" + c.Name,
			Preview:  body,
			Markdown: true,
		})
	}
	return section
}

func (m claudeModel) browserSkillsSection() agentBrowserSection {
	section := agentBrowserSection{Title: "Skills", Color: m.st.P.Mauve}
	for _, s := range m.skills {
		body := readFileOr(s.Path, s.Description)
		section.Items = append(section.Items, agentBrowserItem{
			Label:    s.Name,
			Preview:  body,
			Markdown: true,
		})
	}
	return section
}

// readFileOr returns the file contents at path, or fallback when the
// file is unreadable / empty. Used by the browser sections to read
// slash command + skill .md bodies for the right-pane preview.
func readFileOr(path, fallback string) string {
	if path == "" {
		return fallback
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return fallback
	}
	return string(b)
}

// catalogLoaded reports whether m.catalog has been populated. The
// zero value of claudemodels.Catalog has no Models and a zero
// FetchedAt; we treat any Models slice as "loaded" so the second
// open of pickerCcmuxModel doesn't re-read disk for no reason.
func (m claudeModel) catalogLoaded() bool { return len(m.catalog.Models) > 0 }

// loadCatalog reads the daemon's models.json cache from disk. The
// daemon writes this file on its 24h refresh tick; the TUI just
// consumes it as a static snapshot. A missing/corrupt file falls
// through to the curated in-binary list so the picker is never
// empty on a fresh install or with the daemon stopped.
//
// Synchronous — the cache file is small (a few KB) and this only
// runs once per picker open. No goroutine / tea.Cmd dance.
func (m *claudeModel) loadCatalog() {
	if path, err := claudemodels.CachePath(); err == nil {
		if cat, err := (claudemodels.Cache{Path: path}).Read(); err == nil && len(cat.Models) > 0 {
			m.catalog = cat
			// Merge curated fallback in case the API returned a
			// trimmed set the user's account can see (subscription
			// users without an API key get fallback-only here, but
			// that's fine — it's the same list every release ships).
			m.catalog.Models = claudemodels.Merge(m.catalog.Models, claudemodels.Fallback())
			claudemodels.Sort(m.catalog.Models)
			return
		}
	}
	m.catalog = claudemodels.Catalog{
		Models: claudemodels.Fallback(),
		Source: claudemodels.SourceFallback,
	}
	claudemodels.Sort(m.catalog.Models)
}

// catalogPickerModels returns the rows shown in pickerCcmuxModel,
// prepended with a synthetic "" entry meaning "no pin — inherit
// Claude Code's setting". Returning a real slice (not a pointer)
// keeps callers from accidentally mutating the underlying catalog.
func (m claudeModel) catalogPickerModels() []claudemodels.Model {
	out := make([]claudemodels.Model, 0, len(m.catalog.Models)+1)
	// Sentinel row: no pin. ID="" matches the empty-string check in
	// agent.LaunchCmd, so picking this clears the override.
	out = append(out, claudemodels.Model{ID: "", DisplayName: "(no pin — inherit Claude Code default)"})
	out = append(out, m.catalog.Models...)
	return out
}

// ccmuxModelLabel renders a catalog row's primary cell. Adds a
// " [current]" tag to the row that matches the user's existing pin
// so they can spot it without reading every line.
func ccmuxModelLabel(mdl claudemodels.Model, current string) string {
	label := mdl.ID
	if mdl.DisplayName != "" {
		label = mdl.DisplayName + "  " + mdl.ID
	}
	if mdl.ID == current {
		label += "  [current]"
	}
	return label
}

// ccmuxModelDesc renders the per-row secondary text: source (api vs
// fallback) plus a coarse capability summary. Trimmed deliberately —
// the picker is narrow and a long descriptor line wraps badly.
func ccmuxModelDesc(mdl claudemodels.Model) string {
	if mdl.ID == "" {
		return "Don't set ANTHROPIC_MODEL — let Claude Code choose."
	}
	parts := []string{string(mdl.Source)}
	if mdl.MaxInput >= 1_000_000 {
		parts = append(parts, "1M ctx")
	} else if mdl.MaxInput >= 200_000 {
		parts = append(parts, "200K ctx")
	}
	if mdl.Capabilities["vision"] {
		parts = append(parts, "vision")
	}
	if mdl.Capabilities["thinking_adaptive"] {
		parts = append(parts, "thinking")
	}
	return strings.Join(parts, " · ")
}

// setCcmuxClaudeDefault is the model write — read the on-disk config,
// mutate Claude.DefaultModel, write back. Wrapped here (not inline in
// the picker handler) so a test can verify the precise behavior
// without standing up a Bubble Tea program. Trims whitespace so an
// accidental stray space in a future caller can't leak to the launch
// command (where it would set ANTHROPIC_MODEL=" haiku ").
func setCcmuxClaudeDefault(model string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Claude.DefaultModel = strings.TrimSpace(model)
	return config.Save(cfg)
}

// claudeCcmuxModelChangedMsg is the result of a pickerCcmuxModel
// commit. Mirrors the claudeModelChangedMsg pattern but for the
// ccmux-side default, so the toast/reload flow in Update() can
// branch on a clear discriminated type instead of guessing which
// model was set.
type claudeCcmuxModelChangedMsg struct {
	New string
	Err error
}
