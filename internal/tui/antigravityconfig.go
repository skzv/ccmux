package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/antigravityconfig"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// antigravityConfigModel is the Agents → Antigravity sub-tab. Parallel
// to codexConfigModel — same key bindings, same screen shape, just
// pointed at ~/.gemini/antigravity-cli/settings.json. Antigravity's
// settings file is straight JSON (not TOML like Codex) but the
// antigravityconfig package hides that detail; both packages expose
// the same SetYoloMode / SetEffortLevel / EffectiveYoloMode /
// EffectiveEffortLevel surface.
type antigravityConfigModel struct {
	st       styles.Styles
	settings *antigravityconfig.Settings
	mcp      []antigravityconfig.MCPServer
	paths    antigravityconfig.Locations
	saveMsg  string
	savedAt  time.Time
	browser  agentBrowser
	editor   string
	err      string
}

func newAntigravityConfig(st styles.Styles) antigravityConfigModel {
	m := antigravityConfigModel{st: st, editor: pickEditor(), browser: newAgentBrowser(st)}
	m.reload()
	return m
}

func (m *antigravityConfigModel) reload() {
	if p, err := antigravityconfig.Paths(); err == nil {
		m.paths = p
	}
	if s, err := antigravityconfig.ReadSettings(); err == nil {
		m.settings = s
		m.err = ""
	} else {
		m.err = err.Error()
	}
	m.mcp, _ = antigravityconfig.ListMCPServers()
	m.browser.SetSections("Antigravity configured", m.browserSections())
}

func (m antigravityConfigModel) Update(msg tea.Msg) (antigravityConfigModel, tea.Cmd) {
	if _, ok := msg.(tea.MouseMsg); ok {
		b, cmd, _ := m.browser.Update(msg)
		m.browser = b
		return m, cmd
	}
	if km, ok := msg.(tea.KeyMsg); ok {
		if b, cmd, handled := m.browser.Update(km); handled {
			m.browser = b
			return m, cmd
		}
		switch km.String() {
		case "y":
			cur, _ := antigravityconfig.EffectiveYoloMode()
			if _, err := antigravityconfig.SetYoloMode(!cur); err != nil {
				m.err = "set yolo: " + err.Error()
				m.saveMsg = ""
			} else {
				m.saveMsg = fmt.Sprintf("Antigravity YOLO → %v", !cur)
				m.savedAt = time.Now()
				m.reload()
			}
			return m, nil
		case "r":
			next := nextAntigravityEffort()
			if _, err := antigravityconfig.SetEffortLevel(next); err != nil {
				m.err = "set effort: " + err.Error()
				m.saveMsg = ""
			} else {
				m.saveMsg = "Antigravity effort → " + next
				m.savedAt = time.Now()
				m.reload()
			}
			return m, nil
		case "e":
			return m, func() tea.Msg {
				return openEditorMsg{Editor: m.editor, Path: m.paths.Settings, Source: "agents"}
			}
		}
	}
	return m, nil
}

func nextAntigravityEffort() string {
	cur, _ := antigravityconfig.EffectiveEffortLevel()
	levels := antigravityconfig.KnownEffortLevels()
	for i, l := range levels {
		if l.Value == cur {
			return levels[(i+1)%len(levels)].Value
		}
	}
	if len(levels) > 0 {
		return levels[0].Value
	}
	return ""
}

func (m antigravityConfigModel) View(width, height int) string {
	return m.st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(
		m.ViewBody(width-4, height-2))
}

// ViewBody renders the Antigravity sub-tab's inner content without
// an outer Pane border so agentsModel.View can wrap the whole agent
// surface in one bordered block.
func (m antigravityConfigModel) ViewBody(width, height int) string {
	st := m.st
	narrow := isNarrow(width)
	header := []string{st.Emphasis.Render("Antigravity configuration")}
	if !narrow {
		header = append(header, st.Muted.Render(summarizePath(m.paths.Settings)))
	}
	header = append(header, "")
	if m.err != "" {
		header = append(header, st.StatusError.Render("⚠ "+m.err), "")
	}
	if s := m.settings; s != nil {
		header = append(header,
			fmt.Sprintf("model           %s", emphOrPlaceholder(st, s.Model, "(Antigravity default)")),
			fmt.Sprintf("effort          %s", emphOrPlaceholder(st, s.ReasoningEffort, "(default)")),
		)
		yoloOn, _ := antigravityconfig.EffectiveYoloMode()
		yoloLabel := "off"
		if yoloOn {
			yoloLabel = st.StatusError.Render("YOLO (no approval prompts)")
		}
		header = append(header, fmt.Sprintf("yolo mode       %s", yoloLabel))
	}
	if m.saveMsg != "" && time.Since(m.savedAt) < 3*time.Second {
		header = append(header, "", st.StatusGood.Render("saved ✓  "+m.saveMsg))
	}
	header = append(header, "")
	headerStr := strings.Join(header, "\n")
	headerH := lipgloss.Height(headerStr)

	browserH := height - headerH
	if browserH < 8 {
		browserH = 8
	}
	browserView := m.browser.View(width, browserH)
	return lipgloss.JoinVertical(lipgloss.Left, headerStr, browserView)
}

// browserSections builds the Configured browser sections for the
// Antigravity sub-tab. Today the only surface ccmux parses is
// MCP servers (Gemini CLI / Antigravity stores them under
// `mcpServers` in settings.json); hooks / commands / skills exist in
// the Gemini CLI ecosystem but use file conventions ccmux doesn't
// inspect yet, so they're omitted rather than mocked.
func (m antigravityConfigModel) browserSections() []agentBrowserSection {
	return []agentBrowserSection{m.browserMCPSection()}
}

func (m antigravityConfigModel) browserMCPSection() agentBrowserSection {
	section := agentBrowserSection{Title: "MCP servers", Color: m.st.P.Sky}
	for _, s := range m.mcp {
		preview := []string{s.Name, "", "  type: " + s.Type}
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
			Label:    s.Name,
			Trailing: s.Type,
			Preview:  strings.Join(preview, "\n"),
		})
	}
	return section
}
