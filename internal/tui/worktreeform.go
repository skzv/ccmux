package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tui/styles"
	"github.com/skzv/ccmux/internal/worktree"
)

type newWorktreeSubmitMsg struct {
	Project     string
	ProjectPath string
	Branch      string
	Agent       agent.ID
}

type newWorktreeCancelMsg struct{}

// worktreeFormModel is the modal opened by pressing `w` on a Git project
// in the Projects screen. Creates a worktree at <project>/.worktrees/<branch>
// and launches an isolated agent session there.
type worktreeFormModel struct {
	st          styles.Styles
	projectName string
	projectPath string
	branch      textinput.Model
	focus       int // 0 branch, 1 agent
	agents      []sessionAgentChoice
	agentIdx    int
	err         string
}

func newWorktreeForm(st styles.Styles, p project.Project, commands agent.Commands) worktreeFormModel {
	bi := textinput.New()
	bi.Placeholder = tr("e.g. feat/auth or fix-cache")
	bi.CharLimit = 96
	bi.Width = 44
	bi.Prompt = ""
	bi.Focus()

	installed := agent.AllAvailable(context.Background(), commands)
	if len(installed) == 0 {
		installed = agent.All()
	}
	choices := make([]sessionAgentChoice, 0, len(installed))
	for _, a := range installed {
		choices = append(choices, sessionAgentChoice{ID: a.ID(), Label: a.DisplayName()})
	}
	idx := indexOfDefaultAgent(choices, string(p.Agent))

	return worktreeFormModel{
		st:          st,
		projectName: p.Name,
		projectPath: p.Path,
		branch:      bi,
		focus:       0,
		agents:      choices,
		agentIdx:    idx,
	}
}

func (m worktreeFormModel) Update(msg tea.Msg) (worktreeFormModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			return m, func() tea.Msg { return newWorktreeCancelMsg{} }
		case "tab", "down":
			m.focus = (m.focus + 1) % 2
			if m.focus == 0 {
				m.branch.Focus()
			} else {
				m.branch.Blur()
			}
			return m, nil
		case "shift+tab", "up":
			m.focus = (m.focus + 1) % 2
			if m.focus == 0 {
				m.branch.Focus()
			} else {
				m.branch.Blur()
			}
			return m, nil
		case "left", "h":
			if m.focus == 1 && len(m.agents) > 0 {
				m.agentIdx = (m.agentIdx - 1 + len(m.agents)) % len(m.agents)
				return m, nil
			}
		case "right", "l":
			if m.focus == 1 && len(m.agents) > 0 {
				m.agentIdx = (m.agentIdx + 1) % len(m.agents)
				return m, nil
			}
		case "enter":
			branch := strings.TrimSpace(m.branch.Value())
			if err := worktree.ValidateBranch(branch); err != nil {
				m.err = err.Error()
				return m, nil
			}
			var chosen agent.ID
			if m.agentIdx >= 0 && m.agentIdx < len(m.agents) {
				chosen = m.agents[m.agentIdx].ID
			}
			return m, func() tea.Msg {
				return newWorktreeSubmitMsg{
					Project:     m.projectName,
					ProjectPath: m.projectPath,
					Branch:      branch,
					Agent:       chosen,
				}
			}
		}
	}
	if m.focus == 0 {
		var cmd tea.Cmd
		m.branch, cmd = m.branch.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m worktreeFormModel) View(width int) string {
	st := m.st
	title := st.Emphasis.Render(tr("New worktree") + " · " + m.projectName)
	hint := st.Subtitle.Render(tr("Creates <project>/.worktrees/<branch> and opens a parallel agent session."))

	branchLabel := st.Muted.Render(padLabel(tr("branch"), 8))
	agentLabel := st.Muted.Render(padLabel(tr("agent"), 8))
	branchPrefix := "  "
	agentPrefix := "  "
	if m.focus == 0 {
		branchPrefix = st.Emphasis.Render("▌ ")
	} else {
		agentPrefix = st.Emphasis.Render("▌ ")
	}
	agentName := ""
	if m.agentIdx >= 0 && m.agentIdx < len(m.agents) {
		agentName = m.agents[m.agentIdx].Label
	}
	agentVal := st.Muted.Render("◀ ") + st.Emphasis.Render(agentName) + st.Muted.Render(" ▶")
	keys := st.Muted.Render(tr("tab: next   ←/→: agent   enter: create   esc: cancel"))

	parts := []string{
		title,
		hint,
		"",
		branchLabel + branchPrefix + m.branch.View(),
		agentLabel + agentPrefix + agentVal,
		"",
		keys,
	}
	if m.err != "" {
		parts = append(parts, st.StatusError.Render("⚠ "+m.err))
	}
	frameW := max(24, width-2)
	return st.PaneFocused.Width(frameW).Render(strings.Join(parts, "\n"))
}

// createWorktreeSessionCmd creates the worktree at <project>/.worktrees/<branch>,
// writes its .ccmux/agent sidecar, starts a tmux session if needed, and returns
// projectSessionReadyMsg so App attaches cleanly.
var createWorktreeSessionCmd = func(msg newWorktreeSubmitMsg, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		wt, err := worktree.Create(ctx, msg.ProjectPath, msg.Branch, "")
		if err != nil {
			return toastMsg{Text: fmt.Sprintf(tr("worktree failed: %v"), err), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
		agentID := msg.Agent
		if agentID == "" {
			agentID = project.ReadAgent(msg.ProjectPath)
		}
		_ = project.SetAgent(wt.Path, agentID)
		session := worktree.SessionName(msg.Project, wt.Branch)
		has, err := tmux.Has(ctx, session)
		if err != nil {
			return toastMsg{Text: fmt.Sprintf(tr("worktree failed: %v"), err), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
		if !has {
			launch := agent.LaunchCmd(agentID, false, cfg.AgentCommands())
			if err := tmux.NewWithAgent(ctx, session, wt.Path, launch, string(agentID)); err != nil {
				return toastMsg{Text: fmt.Sprintf(tr("worktree failed: %v"), err), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
		}
		return projectSessionReadyMsg{Project: msg.Project + " (" + wt.Branch + ")", Session: session}
	}
}
