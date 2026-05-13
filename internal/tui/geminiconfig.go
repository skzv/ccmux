package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/geminiconfig"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// geminiConfigModel is the Agents → Gemini sub-tab. Parallel to
// codexConfigModel — same key bindings, same screen shape, just
// pointed at ~/.gemini/settings.json. Gemini's settings file is
// straight JSON (not TOML like Codex) but the geminiconfig package
// hides that detail; both packages expose the same SetYoloMode /
// SetEffortLevel / EffectiveYoloMode / EffectiveEffortLevel surface.
type geminiConfigModel struct {
	st       styles.Styles
	settings *geminiconfig.Settings
	paths    geminiconfig.Locations
	saveMsg  string
	savedAt  time.Time
	editor   string
	err      string
}

func newGeminiConfig(st styles.Styles) geminiConfigModel {
	m := geminiConfigModel{st: st, editor: pickEditor()}
	m.reload()
	return m
}

func (m *geminiConfigModel) reload() {
	if p, err := geminiconfig.Paths(); err == nil {
		m.paths = p
	}
	if s, err := geminiconfig.ReadSettings(); err == nil {
		m.settings = s
		m.err = ""
	} else {
		m.err = err.Error()
	}
}

func (m geminiConfigModel) Update(msg tea.Msg) (geminiConfigModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "y":
			cur, _ := geminiconfig.EffectiveYoloMode()
			if _, err := geminiconfig.SetYoloMode(!cur); err != nil {
				m.err = "set yolo: " + err.Error()
			} else {
				m.saveMsg = fmt.Sprintf("Gemini YOLO → %v", !cur)
				m.savedAt = time.Now()
				m.reload()
			}
			return m, nil
		case "r":
			next := nextGeminiEffort()
			if _, err := geminiconfig.SetEffortLevel(next); err != nil {
				m.err = "set effort: " + err.Error()
			} else {
				m.saveMsg = "Gemini effort → " + next
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

func nextGeminiEffort() string {
	cur, _ := geminiconfig.EffectiveEffortLevel()
	levels := geminiconfig.KnownEffortLevels()
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

func (m geminiConfigModel) View(width, height int) string {
	st := m.st
	rows := []string{
		st.Emphasis.Render("Gemini configuration"),
		st.Muted.Render(m.paths.Settings),
		"",
	}
	if m.err != "" {
		rows = append(rows, st.StatusError.Render("⚠ "+m.err), "")
	}
	if s := m.settings; s != nil {
		rows = append(rows,
			fmt.Sprintf("model           %s", emphOrPlaceholder(st, s.Model, "(Gemini default)")),
			fmt.Sprintf("effort          %s", emphOrPlaceholder(st, s.ReasoningEffort, "(default)")),
		)
		yoloOn, _ := geminiconfig.EffectiveYoloMode()
		yoloLabel := "off"
		if yoloOn {
			yoloLabel = st.StatusError.Render("YOLO (no approval prompts)")
		}
		rows = append(rows, fmt.Sprintf("yolo mode       %s", yoloLabel))
	}

	rows = append(rows, "",
		st.Subtitle.Render("Keys"),
		st.Key.Render("y")+"  toggle YOLO mode",
		st.Key.Render("r")+"  cycle reasoning effort",
		st.Key.Render("e")+"  open settings.json in $EDITOR",
		st.Key.Render("tab")+"  switch agent",
	)

	if m.saveMsg != "" && time.Since(m.savedAt) < 3*time.Second {
		rows = append(rows, "", st.StatusGood.Render("saved ✓  "+m.saveMsg))
	}

	return st.Pane.Width(width - 2).Height(height - 2).Render(strings.Join(rows, "\n"))
}
