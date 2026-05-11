package tui

import (
	"fmt"
	"os"
	"os/exec"
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
// a minimal subset of edits (model selection through a picker);
// deeper edits open the underlying file in $EDITOR.
type claudeModel struct {
	st             styles.Styles
	km             Keymap
	settings       *claudeconfig.Settings
	commands       []claudeconfig.Command
	skills         []claudeconfig.Skill
	model          string
	modelSource    string
	paths          claudeconfig.Locations
	lastBackup     string
	pickerOpen     bool
	pickerCursor   int
	editor         string
}

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

	case tea.KeyMsg:
		if m.pickerOpen {
			return m.updatePicker(msg)
		}
		switch msg.String() {
		case "m":
			m.pickerOpen = true
			m.pickerCursor = 0
			cur := strings.ToLower(strings.TrimSpace(m.settings.Model))
			for i, opt := range claudeconfig.KnownModels() {
				if opt.Alias == cur {
					m.pickerCursor = i
					break
				}
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
	opts := claudeconfig.KnownModels()
	switch msg.String() {
	case "esc":
		m.pickerOpen = false
	case "up", "k":
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case "down", "j":
		if m.pickerCursor < len(opts)-1 {
			m.pickerCursor++
		}
	case "enter":
		chosen := opts[m.pickerCursor].Alias
		m.pickerOpen = false
		return m, func() tea.Msg {
			backup, err := claudeconfig.SetModel(chosen)
			return claudeModelChangedMsg{New: chosen, Backup: backup, Err: err}
		}
	}
	return m, nil
}

func (m claudeModel) View(width, height int) string {
	if m.pickerOpen {
		return m.viewPicker(width, height)
	}
	return m.viewMain(width, height)
}

func (m claudeModel) viewMain(width, height int) string {
	st := m.st
	lines := []string{
		st.Emphasis.Render("Claude Code Configuration"),
		st.Muted.Render("settings: " + m.paths.Settings),
		"",
		m.renderModelBlock(),
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
		"",
		st.Subtitle.Render("Keys"),
		"  " + st.Key.Render("m") + "  pick default model       " + st.Key.Render("c") + "  edit global CLAUDE.md",
		"  " + st.Key.Render("j") + "  edit settings.json       " + st.Key.Render("4") + "  open project notes",
	}
	if m.lastBackup != "" {
		lines = append(lines, "",
			st.Muted.Render("last write backed up to "+summarizePath(m.lastBackup)),
		)
	}
	return st.Pane.Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
}

func (m claudeModel) renderModelBlock() string {
	st := m.st
	hint := "(set in " + m.modelSource + ")"
	return strings.Join([]string{
		st.Subtitle.Render("Default model"),
		"  " + st.Emphasis.Render(m.model) + "  " + st.Muted.Render(hint),
		st.Muted.Render("  press " + st.Key.Render("m") + " to change"),
	}, "\n")
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
	return strings.Join([]string{
		st.Subtitle.Render("Permissions"),
		fmt.Sprintf("  allow: %d pattern(s)", len(m.settings.Permissions.Allow)),
		fmt.Sprintf("  deny:  %d pattern(s)", len(m.settings.Permissions.Deny)),
		st.Muted.Render("  edit with " + st.Key.Render("j") + " (opens settings.json)"),
	}, "\n")
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
	opts := claudeconfig.KnownModels()
	lines := []string{
		st.Emphasis.Render("Pick default model"),
		st.Subtitle.Render("Writes to " + m.paths.Settings + " (backed up first)."),
		"",
	}
	for i, o := range opts {
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

// openClaudeFileCmd creates the target file if requested, then exec's
// $EDITOR on it. Re-reads the screen state on return.
func openClaudeFileCmd(editor, path string, createIfMissing bool) tea.Cmd {
	if createIfMissing {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			_ = os.WriteFile(path, []byte("# CLAUDE.md\n\n(global Claude Code instructions for this host)\n"), 0o644)
		}
	}
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return toastMsg{Text: "editor: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
		return claudeReloadMsg{}
	})
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
