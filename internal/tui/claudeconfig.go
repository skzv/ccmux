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
	// catalog is the live model list shown by the model picker. Loaded
	// lazily on first open from the daemon's on-disk cache; falls back
	// to the curated in-binary list when the cache file isn't there
	// (fresh install, daemon not running, etc).
	catalog claudemodels.Catalog
	browser agentBrowser
	editor  string
	narrow  bool // terminal is below the layout breakpoint

	// Unified vertical navigation. The Claude screen is two stacked
	// zones: the settings rows at the top (model / effort / thinking /
	// yolo / CLAUDE.md / settings.json) and the configured-items
	// browser below. focusTop reports which zone owns up/down/enter;
	// rowCursor is the selected settings row while focusTop is true.
	// Arrowing down past the last settings row hands focus to the
	// browser; arrowing up at the browser's first item hands it back.
	// This is what makes "just use the arrow keys to move through every
	// setting" work — before, the top rows weren't selectable at all.
	rowCursor int
	focusTop  bool
}

// Claude settings rows that the unified cursor walks, in render order.
// Enter on the selected row runs its action (open a picker, toggle a
// flag, or open a file in $EDITOR); the dedicated letter keys still
// work as shortcuts regardless of where the cursor sits.
const (
	claudeRowModel = iota
	claudeRowEffort
	claudeRowAlwaysThinking
	claudeRowYolo
	claudeRowClaudeMd
	claudeRowSettings
	claudeActionRowCount // sentinel: number of settings rows
)

// pickerKind identifies which modal picker is currently open on the
// Claude screen. pickerNone means no modal is showing.
type pickerKind int

const (
	pickerNone pickerKind = iota
	// pickerModel is the unified model picker. It lists the live
	// catalog (full IDs discovered from `claude`, or the curated
	// fallback) plus the family aliases, and a pick writes BOTH Claude
	// Code's ~/.claude/settings.json model AND ccmux's [claude]
	// default_model pin — the latter re-exported as ANTHROPIC_MODEL at
	// launch so the pick takes effect for ccmux sessions even when the
	// shell already exports ANTHROPIC_MODEL.
	pickerModel
	pickerEffort
)

func newClaude(st styles.Styles, km Keymap) claudeModel {
	m := claudeModel{st: st, km: km, editor: pickEditor(), browser: newAgentBrowser(st), focusTop: true}
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
		// Unified vertical navigation across the two stacked zones.
		// focusTop = the settings rows own up/down/enter; otherwise the
		// browser does. The zones hand off at their boundaries so a
		// single stream of arrow presses walks every setting top to
		// bottom and back.
		switch msg.String() {
		case "down", "j":
			if m.focusTop {
				if m.rowCursor < claudeActionRowCount-1 {
					m.rowCursor++
				} else if m.browser.HasItems() {
					m.focusTop = false
					m.browser.GotoFirstItem()
				}
				return m, nil
			}
			b, cmd, _ := m.browser.Update(msg)
			m.browser = b
			return m, cmd
		case "up", "k":
			if m.focusTop {
				if m.rowCursor > 0 {
					m.rowCursor--
				}
				return m, nil
			}
			if m.browser.AtFirstItem() {
				m.focusTop = true
				m.rowCursor = claudeActionRowCount - 1
				return m, nil
			}
			b, cmd, _ := m.browser.Update(msg)
			m.browser = b
			return m, cmd
		case "enter":
			if m.focusTop {
				return m.activateRow(m.rowCursor)
			}
			b, cmd, _ := m.browser.Update(msg)
			m.browser = b
			return m, cmd
		}
		// Non-vertical browser keys (left/right pane toggle, g/G, page
		// scrolling) only apply while the browser is focused.
		if !m.focusTop {
			if b, cmd, handled := m.browser.Update(msg); handled {
				m.browser = b
				return m, cmd
			}
		}
		// Dedicated letter shortcuts — work regardless of which zone has
		// focus, so muscle memory (m for model, e for effort, …) keeps
		// working without arrowing to the row first. They also move the
		// row cursor to the row they act on so the highlight follows.
		switch msg.String() {
		case "m", "M":
			// One unified model picker now. `M` used to open a separate
			// "ccmux pin" picker; both keys open the same picker so old
			// muscle memory keeps working without the confusing split.
			m.rowCursor, m.focusTop = claudeRowModel, true
			return m.openModelPicker(), nil
		case "e":
			m.rowCursor, m.focusTop = claudeRowEffort, true
			return m.openEffortPicker(), nil
		case "a":
			m.rowCursor, m.focusTop = claudeRowAlwaysThinking, true
			return m, m.toggleAlwaysThinkingCmd()
		case "y":
			m.rowCursor, m.focusTop = claudeRowYolo, true
			return m, m.toggleYoloCmd()
		case "c":
			m.rowCursor, m.focusTop = claudeRowClaudeMd, true
			return m, openClaudeFileCmd(m.editor, m.paths.GlobalCLAUDEMd, true)
		}
	}
	return m, nil
}

// activateRow runs the action for a settings row when the user presses
// Enter on it. Mirrors the dedicated letter shortcuts so the two paths
// (arrow-to-row + Enter, or the letter key) always do the same thing.
func (m claudeModel) activateRow(row int) (claudeModel, tea.Cmd) {
	switch row {
	case claudeRowModel:
		return m.openModelPicker(), nil
	case claudeRowEffort:
		return m.openEffortPicker(), nil
	case claudeRowAlwaysThinking:
		return m, m.toggleAlwaysThinkingCmd()
	case claudeRowYolo:
		return m, m.toggleYoloCmd()
	case claudeRowClaudeMd:
		return m, openClaudeFileCmd(m.editor, m.paths.GlobalCLAUDEMd, true)
	case claudeRowSettings:
		return m, openClaudeFileCmd(m.editor, m.paths.Settings, false)
	}
	return m, nil
}

// openModelPicker opens the unified model picker. It loads the live
// catalog (so the list reflects what `claude` actually offers — current
// models like claude-opus-4-8, not a hardcoded alias list) and
// pre-positions the cursor on the model ccmux will currently launch
// with: the ccmux pin if set, otherwise the effective model (which may
// be a shell $ANTHROPIC_MODEL or settings.json). Reading the effective
// model — not settings.Model — matters: when $ANTHROPIC_MODEL overrides,
// the picker would otherwise open on "Inherit", the user re-picks the
// model they already use, and it looks broken.
func (m claudeModel) openModelPicker() claudeModel {
	m.picker = pickerModel
	m.pickerCursor = 0
	if !m.catalogLoaded() {
		m.loadCatalog()
	}
	cur := strings.TrimSpace(m.ccmuxDefaultModel)
	if cur == "" {
		cur = strings.TrimSpace(m.model)
	}
	for i, c := range m.unifiedModelChoices() {
		if cur != "" && (c.Settings == cur || c.Pin == cur) {
			m.pickerCursor = i
			break
		}
	}
	return m
}

// openEffortPicker opens the reasoning-effort picker, pre-positioned on
// the current value. Guards a nil settings (reload leaves it nil on a
// malformed settings.json) so opening the picker on a broken config
// can't nil-deref — exactly when the user came here to fix it.
func (m claudeModel) openEffortPicker() claudeModel {
	m.picker = pickerEffort
	m.pickerCursor = 0
	cur := ""
	if m.settings != nil {
		cur = strings.ToLower(strings.TrimSpace(m.settings.EffortLevel))
	}
	for i, opt := range claudeconfig.KnownEffortLevels() {
		if opt.Value == cur {
			m.pickerCursor = i
			break
		}
	}
	return m
}

// toggleAlwaysThinkingCmd / toggleYoloCmd return the persist-and-report
// command for the two boolean settings rows. Extracted so the Enter
// activation and the letter shortcut share one implementation.
func (m claudeModel) toggleAlwaysThinkingCmd() tea.Cmd {
	toggled := !m.alwaysThinking
	return func() tea.Msg {
		backup, err := claudeconfig.SetAlwaysThinking(toggled)
		return claudeAlwaysThinkingChangedMsg{New: toggled, Backup: backup, Err: err}
	}
}

func (m claudeModel) toggleYoloCmd() tea.Cmd {
	toggled := !m.yolo
	return func() tea.Msg {
		backup, err := claudeconfig.SetYoloMode(toggled)
		return claudeYoloChangedMsg{New: toggled, Backup: backup, Err: err}
	}
}

func (m claudeModel) updatePicker(msg tea.KeyMsg) (claudeModel, tea.Cmd) {
	var optsLen int
	switch m.picker {
	case pickerModel:
		optsLen = len(m.unifiedModelChoices())
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
			choices := m.unifiedModelChoices()
			if cursor >= len(choices) {
				return m, nil
			}
			return m, applyModelChoiceCmd(choices[cursor])
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

// PickerOpen reports whether a modal picker (model / effort / ccmux-
// model) is currently showing. agentsModel.View consults this to render
// the centered picker overlay instead of the normal bordered body. The
// body render path (ViewBody) deliberately omits the picker so the
// agents screen owns its own chrome — but that means the picker only
// renders through View(), and the agents screen must call View() when
// this is true. Without that, pressing `m` sets the picker state but it
// never appears (the bug that made the model picker look dead).
func (m claudeModel) PickerOpen() bool { return m.picker != pickerNone }

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
	// When the top zone has focus, highlight the selected settings row;
	// otherwise the browser owns the visible selection so no top row is
	// marked (selected = -1).
	selected := -1
	if m.focusTop {
		selected = m.rowCursor
	}
	header := []string{
		st.Emphasis.Render("Claude Code Configuration"),
		"",
		st.AgentAccent(agent.IDClaude).Render("Defaults"),
	}
	header = append(header, m.renderDefaultsRows(selected)...)
	header = append(header,
		"",
		st.AgentAccent(agent.IDClaude).Render("Config files"),
	)
	header = append(header, m.renderConfigFilesRows(selected)...)
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
func (m claudeModel) renderDefaultsRows(selected int) []string {
	st := m.st
	thinkLabel := "off"
	if m.alwaysThinking {
		thinkLabel = "on"
	}
	yoloLabel := "off"
	if m.yolo {
		yoloLabel = "on"
	}
	row := func(idx int, label, value, source string) string {
		out := m.rowMarker(idx, selected) + fmt.Sprintf("%-18s", label) + st.Emphasis.Render(value)
		if source != "" {
			out += "  " + st.Muted.Render("(from "+source+")")
		}
		return out
	}
	modelValue, modelSource := m.modelRowDisplay()
	return []string{
		row(claudeRowModel, "model", modelValue, modelSource),
		row(claudeRowEffort, "effort", m.effort, m.effortSource),
		row(claudeRowAlwaysThinking, "always-thinking", thinkLabel, ""),
		row(claudeRowYolo, "yolo mode", yoloLabel, m.yoloSource),
	}
}

// modelRowDisplay returns the value + source shown on the model row.
// It reflects what ccmux will actually LAUNCH with, which is not the
// same as Claude Code's effective model when either a ccmux pin or a
// shell $ANTHROPIC_MODEL is in play. Precedence, highest first:
//
//   - ccmux pin ([claude] default_model) → ccmux re-exports this as
//     ANTHROPIC_MODEL at launch, so it wins for ccmux sessions even
//     over a shell ANTHROPIC_MODEL. Shown as "ccmux pin".
//   - whatever EffectiveModel resolved (a shell $ANTHROPIC_MODEL, or
//     settings.json, or the Claude default) — shown with that source,
//     and the shell case is flagged as overriding so the user isn't
//     surprised the model row doesn't match their settings.json.
func (m claudeModel) modelRowDisplay() (value, source string) {
	if p := strings.TrimSpace(m.ccmuxDefaultModel); p != "" {
		return p, "ccmux pin → ANTHROPIC_MODEL"
	}
	if m.modelSource == "$ANTHROPIC_MODEL" {
		return m.model, "$ANTHROPIC_MODEL in shell — overrides settings.json"
	}
	return m.model, m.modelSource
}

// rowMarker returns the 2-cell left gutter for a settings row: an
// accent "▌ " bar when the row is the active selection, two spaces
// otherwise. Same width either way so the value columns stay aligned.
func (m claudeModel) rowMarker(idx, selected int) string {
	if idx == selected {
		return m.st.AgentAccent(agent.IDClaude).Render("▌") + " "
	}
	return "  "
}

// renderConfigFilesRows lists the two Claude config-file paths. Path
// column is muted so the file name is the visual anchor; the rows are
// selectable (Enter opens the file in $EDITOR) and `c` still opens
// CLAUDE.md directly.
func (m claudeModel) renderConfigFilesRows(selected int) []string {
	st := m.st
	row := func(idx int, name, path string) string {
		return m.rowMarker(idx, selected) + fmt.Sprintf("%-18s", name) + st.Muted.Render(summarizePath(path))
	}
	return []string{
		row(claudeRowClaudeMd, "CLAUDE.md", m.paths.GlobalCLAUDEMd),
		row(claudeRowSettings, "settings.json", m.paths.Settings),
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
	var title, subtitle string
	var rows []pickerRow
	switch m.picker {
	case pickerModel:
		title = "Pick model"
		subtitle = "Sets Claude Code's default (settings.json) AND pins it for ccmux-launched sessions."
		for _, c := range m.unifiedModelChoices() {
			rows = append(rows, pickerRow{Label: c.Label, Desc: c.Desc})
		}
	case pickerEffort:
		title = "Pick reasoning effort"
		subtitle = "Writes to " + m.paths.Settings + " (backed up first)."
		for _, o := range claudeconfig.KnownEffortLevels() {
			rows = append(rows, pickerRow{Label: o.Label, Desc: o.Desc})
		}
	}
	lines := []string{
		st.Emphasis.Render(title),
		st.Subtitle.Render(subtitle),
	}
	// When the user's SHELL exports ANTHROPIC_MODEL, it sits above
	// settings.json in Claude Code's precedence — so a settings.json
	// pick alone would do nothing. The unified picker also writes the
	// ccmux pin (which ccmux re-exports at launch, overriding the
	// shell value for ccmux sessions), so a pick DOES take effect here.
	// We still surface the shell var so the user knows to unset it if
	// they want the change to apply everywhere, not just ccmux sessions.
	if m.picker == pickerModel && m.modelSource == "$ANTHROPIC_MODEL" {
		lines = append(lines,
			st.StatusWarning.Render("⚠ Your shell exports ANTHROPIC_MODEL="+m.model+"."),
			st.Muted.Render("  Your pick is pinned for ccmux sessions (takes effect here)."),
			st.Muted.Render("  To change it everywhere, unset ANTHROPIC_MODEL in your shell (e.g. ~/.zshrc)."),
		)
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
// open of the model picker doesn't re-read disk for no reason.
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

// modelChoice is one row in the unified model picker. It captures both
// targets a pick writes: Settings → ~/.claude/settings.json `model`
// (Claude Code's global default), and Pin → ccmux's [claude]
// default_model, which ccmux exports as ANTHROPIC_MODEL when it
// launches a claude session. Writing both is what makes a pick take
// effect even when the user's shell already exports ANTHROPIC_MODEL —
// which sits above settings.json in Claude Code's precedence and would
// otherwise silently shadow it (the "my change does nothing" gotcha
// behind merging the old m/M pickers into one).
type modelChoice struct {
	Label    string
	Desc     string
	Settings string // settings.json model value (alias or full ID); "" clears it
	Pin      string // ccmux ANTHROPIC_MODEL pin (full ID); "" clears the pin
}

// toastValue is the human-readable summary of what a pick did, for the
// success toast.
func (c modelChoice) toastValue() string {
	if c.Settings == "" && c.Pin == "" {
		return "(no override — inherit Claude Code default)"
	}
	base := c.Settings
	if base == "" {
		base = c.Pin
	}
	if c.Pin != "" {
		base += " (pinned for ccmux sessions)"
	}
	return base
}

// unifiedModelChoices builds the model-picker rows: a clear/inherit
// sentinel, every model from the live catalog (full IDs like
// claude-opus-4-8, discovered from `claude -p` by the daemon or the
// curated fallback), then the stable family aliases (opus / sonnet / …)
// for "always track the latest." Full-ID rows pin ANTHROPIC_MODEL so
// the pick wins regardless of the shell; alias rows write settings.json
// only — they resolve to "latest" at the Claude layer, and a pin would
// freeze them to one version.
func (m claudeModel) unifiedModelChoices() []modelChoice {
	out := []modelChoice{{
		Label:    "Inherit / clear override",
		Desc:     "Use Claude Code's default; clear ccmux's pin",
		Settings: "", Pin: "",
	}}
	for _, mdl := range m.catalog.Models {
		label := mdl.ID
		if mdl.DisplayName != "" {
			label = mdl.DisplayName + "  " + mdl.ID
		}
		out = append(out, modelChoice{
			Label:    label,
			Desc:     ccmuxModelDesc(mdl),
			Settings: mdl.ID,
			Pin:      mdl.ID,
		})
	}
	for _, a := range claudeconfig.KnownModels() {
		if a.Alias == "" {
			continue // our own sentinel above already covers "inherit"
		}
		out = append(out, modelChoice{
			Label:    a.Alias + "  (always latest " + a.Alias + ")",
			Desc:     "Tracks Claude Code's current " + a.Alias + "; settings.json only",
			Settings: a.Alias,
			Pin:      "", // aliases track latest — pinning would freeze them
		})
	}
	return out
}

// applyModelChoiceCmd writes both targets of a pick: settings.json
// `model` AND ccmux's pin. Doing both in one command (rather than two
// chained messages) keeps the success/failure reporting atomic — the
// user sees one toast, and a failure on either write surfaces.
func applyModelChoiceCmd(c modelChoice) tea.Cmd {
	return func() tea.Msg {
		backup, err := claudeconfig.SetModel(c.Settings)
		if err != nil {
			return claudeModelChangedMsg{New: c.Settings, Err: err}
		}
		if perr := setCcmuxClaudeDefault(c.Pin); perr != nil {
			return claudeModelChangedMsg{New: c.Settings, Backup: backup, Err: perr}
		}
		return claudeModelChangedMsg{New: c.toastValue(), Backup: backup}
	}
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
