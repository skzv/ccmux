package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/codexconfig"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// codexConfigModel is the Agents → Codex sub-tab. Smaller than its
// Claude sibling because Codex's TUI config surface is narrower today:
// we surface the effective model + reasoning effort + YOLO state, and
// expose the two ccmux-managed knobs (yolo / effort) as single-key
// toggles. Deeper edits (MCP servers, plugins, marketplaces) open
// ~/.codex/config.toml in $EDITOR.
//
// Why we don't ship a model picker for Codex: Codex's catalog moves
// faster than Anthropic's and we don't have a hand-curated short
// list yet. The model field renders read-only; the user edits it
// in $EDITOR when they want a change.
type codexConfigModel struct {
	st       styles.Styles
	settings *codexconfig.Settings
	hooks    codexconfig.HooksFile
	mcp      []codexconfig.MCPServer
	prompts  []codexconfig.Prompt
	rules    []codexconfig.Rule
	paths    codexconfig.Locations
	saveMsg  string // transient "saved ✓" flash
	savedAt  time.Time
	browser  agentBrowser
	editor   string
	err      string
}

func newCodexConfig(st styles.Styles) codexConfigModel {
	m := codexConfigModel{st: st, editor: pickEditor(), browser: newAgentBrowser(st)}
	m.reload()
	return m
}

func (m *codexConfigModel) reload() {
	if p, err := codexconfig.Paths(); err == nil {
		m.paths = p
	}
	if s, err := codexconfig.ReadSettings(); err == nil {
		m.settings = s
		m.err = ""
	} else {
		m.err = err.Error()
	}
	m.hooks, _ = codexconfig.ReadHooks()
	m.mcp, _ = codexconfig.ListMCPServers()
	m.prompts, _ = codexconfig.ListPrompts()
	m.rules, _ = codexconfig.ListRules()
	m.browser.SetSections("Codex configured", m.browserSections())
}

func (m codexConfigModel) Update(msg tea.Msg) (codexConfigModel, tea.Cmd) {
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
			// Toggle YOLO. PR #8 added SetYoloMode which writes both
			// approval_policy + sandbox_mode atomically and backs up
			// config.toml first.
			cur, _ := codexconfig.EffectiveYoloMode()
			if _, err := codexconfig.SetYoloMode(!cur); err != nil {
				m.err = "set yolo: " + err.Error()
				m.saveMsg = ""
			} else {
				m.saveMsg = fmt.Sprintf("Codex YOLO → %v", !cur)
				m.savedAt = time.Now()
				m.reload()
			}
			return m, nil
		case "r":
			// Cycle reasoning effort. Walk KnownEffortLevels and pick
			// the next; reuse the same approach Claude's UI takes.
			next := nextCodexEffort()
			if _, err := codexconfig.SetEffortLevel(next); err != nil {
				m.err = "set effort: " + err.Error()
				m.saveMsg = ""
			} else {
				m.saveMsg = "Codex effort → " + next
				m.savedAt = time.Now()
				m.reload()
			}
			return m, nil
		case "e":
			// Open ~/.codex/config.toml in $EDITOR. App handles the
			// suspend + reload-on-return via openEditorMsg.
			return m, func() tea.Msg {
				return openEditorMsg{Editor: m.editor, Path: m.paths.Config, Source: "agents"}
			}
		}
	}
	return m, nil
}

// nextCodexEffort cycles through codexconfig.KnownEffortLevels in
// order, wrapping back to the start. Stable order means pressing `r`
// repeatedly walks predictably rather than randomly.
func nextCodexEffort() string {
	cur, _ := codexconfig.EffectiveEffortLevel()
	levels := codexconfig.KnownEffortLevels()
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

func (m codexConfigModel) View(width, height int) string {
	return m.st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(
		m.ViewBody(width-4, height-2))
}

// ViewBody renders the Codex sub-tab's inner content without the
// outer Pane border. agentsModel.View owns the bordered chrome so
// the sub-tab row + body share one continuous block.
func (m codexConfigModel) ViewBody(width, height int) string {
	st := m.st
	narrow := isNarrow(width)
	header := []string{st.Emphasis.Render("Codex configuration")}
	if !narrow {
		header = append(header, st.Muted.Render(summarizePath(m.paths.Config)))
	}
	header = append(header, "")
	if m.err != "" {
		header = append(header, st.StatusError.Render("⚠ "+m.err), "")
	}
	if s := m.settings; s != nil {
		header = append(header,
			fmt.Sprintf("model           %s", emphOrPlaceholder(st, s.Model, "(Codex default)")),
			fmt.Sprintf("effort          %s", emphOrPlaceholder(st, s.ModelReasoningEffort, "(default)")),
			fmt.Sprintf("approval        %s", emphOrPlaceholder(st, s.ApprovalPolicy, "(default)")),
			fmt.Sprintf("sandbox         %s", emphOrPlaceholder(st, s.SandboxMode, "(default)")),
		)
		yoloOn, _ := codexconfig.EffectiveYoloMode()
		yoloLabel := "off"
		if yoloOn {
			yoloLabel = st.StatusError.Render("YOLO (no approval prompts, full filesystem)")
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

// emphOrPlaceholder renders `v` with Emphasis if non-empty, otherwise
// renders `placeholder` in muted style. Centralized so the empty-row
// look is consistent across the codex/antigravity screens.
func emphOrPlaceholder(st styles.Styles, v, placeholder string) string {
	if strings.TrimSpace(v) == "" {
		return st.Muted.Render(placeholder)
	}
	return st.Emphasis.Render(v)
}

// browserSections builds the Configured browser sections for the
// Codex sub-tab. Mirrors claudeModel.browserSections in section order
// — Hooks (~/.codex/hooks.json), MCP (config.toml [mcp_servers]),
// Prompts (~/.codex/prompts/), Rules (~/.codex/rules/) — so the
// per-agent browsers feel like the same surface.
func (m codexConfigModel) browserSections() []agentBrowserSection {
	out := []agentBrowserSection{
		m.browserHooksSection(),
		m.browserMCPSection(),
		m.browserPromptsSection(),
		m.browserRulesSection(),
	}
	return out
}

func (m codexConfigModel) browserHooksSection() agentBrowserSection {
	section := agentBrowserSection{Title: "Hooks", Color: m.st.P.Peach}
	if len(m.hooks.Hooks) == 0 {
		return section
	}
	events := make([]string, 0, len(m.hooks.Hooks))
	for k := range m.hooks.Hooks {
		events = append(events, k)
	}
	// Same preferred-then-alphabetical ordering Claude uses.
	preferred := []string{"SessionStart", "UserPromptSubmit", "PermissionRequest", "Stop"}
	seen := map[string]bool{}
	ordered := []string{}
	for _, p := range preferred {
		if _, ok := m.hooks.Hooks[p]; ok {
			ordered = append(ordered, p)
			seen[p] = true
		}
	}
	rest := []string{}
	for _, e := range events {
		if !seen[e] {
			rest = append(rest, e)
		}
	}
	sort.Strings(rest)
	ordered = append(ordered, rest...)
	for _, event := range ordered {
		count := 0
		preview := []string{event, ""}
		for _, g := range m.hooks.Hooks[event] {
			for _, h := range g.Hooks {
				count++
				preview = append(preview, "  command: "+h.Command)
				if h.Timeout > 0 {
					preview = append(preview, fmt.Sprintf("  timeout: %ds", h.Timeout))
				}
				if h.StatusMessage != "" {
					preview = append(preview, "  status: "+h.StatusMessage)
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

func (m codexConfigModel) browserMCPSection() agentBrowserSection {
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

func (m codexConfigModel) browserPromptsSection() agentBrowserSection {
	section := agentBrowserSection{Title: "Commands", Color: m.st.P.Green}
	for _, p := range m.prompts {
		section.Items = append(section.Items, agentBrowserItem{
			Label:    "/" + p.Name,
			Preview:  p.Body,
			Markdown: true,
		})
	}
	return section
}

func (m codexConfigModel) browserRulesSection() agentBrowserSection {
	section := agentBrowserSection{Title: "Rules", Color: m.st.P.Mauve}
	for _, r := range m.rules {
		// Rule files use the .rules extension but the body is markdown-
		// adjacent (frontmatter + freeform prose); Glamour renders it
		// the same way it renders SKILL.md.
		section.Items = append(section.Items, agentBrowserItem{
			Label:    r.Name,
			Preview:  r.Body,
			Markdown: true,
		})
	}
	return section
}
