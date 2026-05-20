package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	paths    codexconfig.Locations
	saveMsg  string // transient "saved ✓" flash
	savedAt  time.Time
	editor   string
	err      string
}

func newCodexConfig(st styles.Styles) codexConfigModel {
	m := codexConfigModel{st: st, editor: pickEditor()}
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
}

func (m codexConfigModel) Update(msg tea.Msg) (codexConfigModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "y":
			// Toggle YOLO. PR #8 added SetYoloMode which writes both
			// approval_policy + sandbox_mode atomically and backs up
			// config.toml first.
			cur, _ := codexconfig.EffectiveYoloMode()
			if _, err := codexconfig.SetYoloMode(!cur); err != nil {
				m.err = "set yolo: " + err.Error()
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
	st := m.st
	narrow := isNarrow(width)
	rows := []string{st.Emphasis.Render("Codex configuration")}
	// The config-file path is T2 — drop it on narrow.
	if !narrow {
		rows = append(rows, st.Muted.Render(m.paths.Config))
	}
	rows = append(rows, "")
	if m.err != "" {
		rows = append(rows, st.StatusError.Render("⚠ "+m.err), "")
	}
	if s := m.settings; s != nil {
		// Model + effort + yolo block.
		rows = append(rows,
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
		rows = append(rows, fmt.Sprintf("yolo mode       %s", yoloLabel))
	}

	// The Keys cheatsheet is T2 — dropped on narrow.
	if !narrow {
		rows = append(rows, "",
			st.Subtitle.Render("Keys"),
			st.Key.Render("y")+"  toggle YOLO mode (writes approval_policy + sandbox_mode)",
			st.Key.Render("r")+"  cycle reasoning effort",
			st.Key.Render("e")+"  open config.toml in $EDITOR",
			st.Key.Render("tab")+"  switch agent",
		)
	}

	if m.saveMsg != "" && time.Since(m.savedAt) < 3*time.Second {
		rows = append(rows, "", st.StatusGood.Render("saved ✓  "+m.saveMsg))
	}

	return st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(strings.Join(rows, "\n"))
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
