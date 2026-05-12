package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/scaffold"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// projectsModel is the Projects screen. Two states:
//   - listing: tree of projects with detail pane
//   - form != nil: modal for creating a new project
type projectsModel struct {
	st       styles.Styles
	km       Keymap
	projects []project.Project
	cursor   int
	form     *newProjectFormModel

	// hosts is the live reachable-peer list, fed in from App on every
	// sessionsLoadedMsg. Snapshot into the form at "n"-press time so
	// the picker shows what the user was looking at.
	hosts []hostStatus
}

func newProjects(st styles.Styles, km Keymap) projectsModel {
	return projectsModel{st: st, km: km}
}

func (m *projectsModel) SetProjects(p []project.Project) {
	m.projects = p
	if m.cursor >= len(p) {
		m.cursor = max0(len(p) - 1)
	}
}

// SetHosts is called from App so the "n" form can populate its device
// picker with reachable peers at form-open time.
func (m *projectsModel) SetHosts(h []hostStatus) {
	m.hosts = h
}

func (m projectsModel) Update(msg tea.Msg) (projectsModel, tea.Cmd) {
	// Modal mode: route everything to the form.
	if m.form != nil {
		switch msg := msg.(type) {
		case newProjectCancelMsg:
			m.form = nil
			return m, nil
		case newProjectSubmitMsg:
			// Drop the form, kick off scaffold+session start as a tea.Cmd.
			m.form = nil
			return m, scaffoldAndStartCmd(msg)
		}
		f, cmd := m.form.Update(msg)
		m.form = &f
		return m, cmd
	}

	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case keyMatches(km, m.km.NewItem):
			f := newNewProjectForm(m.st, m.hosts)
			m.form = &f
			return m, tea.Batch(textInputBlink())
		case km.String() == "u":
			// Upgrade current working directory (non-destructive).
			return m, upgradeCwdCmd()
		case keyMatches(km, m.km.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case keyMatches(km, m.km.Down):
			if m.cursor < len(m.projects)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m projectsModel) View(width, height int) string {
	if m.form != nil {
		// Show form centered with project list dimmed behind it.
		formW := minInt(80, width-4)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.form.View(formW))
	}
	if isNarrow(width) {
		return m.renderList(width, height)
	}
	leftW := width * 2 / 3
	rightW := width - leftW - 1
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderList(leftW, height),
		" ",
		m.renderDetail(rightW, height),
	)
}

func (m projectsModel) renderList(width, height int) string {
	header := m.st.Emphasis.Render("Projects") + "  " + m.st.Muted.Render("(n: new   u: upgrade cwd   enter: attach)")
	if len(m.projects) == 0 {
		body := lipgloss.JoinVertical(lipgloss.Left,
			header,
			"",
			m.st.Muted.Render("No projects found under your projects root."),
			"",
			"Press "+m.st.Key.Render("n")+" to scaffold a new one.",
		)
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(body)
	}
	rows := []string{header, ""}
	currentHost := ""
	for i, p := range m.projects {
		host := projectHost(p)
		if host != currentHost {
			if i > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, m.st.Subtitle.Render("on "+host))
			currentHost = host
		}
		marks := []string{}
		if p.HasGit {
			marks = append(marks, "git")
		}
		if p.HasCM {
			marks = append(marks, "CLAUDE")
		}
		if p.HasDocs {
			marks = append(marks, "docs/")
		}
		tail := ""
		if len(marks) > 0 {
			tail = "   " + m.st.Muted.Render(strings.Join(marks, " · "))
		}
		line := "  " + p.Name + tail
		if i == m.cursor {
			line = m.st.ListItemSelected.Render(line)
		} else {
			line = m.st.ListItem.Render(line)
		}
		rows = append(rows, line)
	}
	return m.st.PaneFocused.Width(width - 2).Height(height - 2).Render(strings.Join(rows, "\n"))
}

func (m projectsModel) renderDetail(width, height int) string {
	if len(m.projects) == 0 || m.cursor < 0 || m.cursor >= len(m.projects) {
		return m.st.Pane.Width(width - 2).Height(height - 2).Render(m.st.Muted.Render("No selection."))
	}
	p := m.projects[m.cursor]
	host := projectHost(p)
	enterDesc := "attach or create session locally"
	if host != "local" {
		enterDesc = "create session on " + host + " (via ccmuxd), then ssh-attach"
	}
	lines := []string{
		m.st.Emphasis.Render(p.Name) + "   " + m.st.Muted.Render("on "+host),
		m.st.Muted.Render(p.Path),
		"",
		"session name  " + m.st.Emphasis.Render(p.SessionName()),
		"",
		m.st.Subtitle.Render("Keys"),
		m.st.Key.Render("enter") + "  " + enterDesc,
		m.st.Key.Render("n") + "      new project (modal form)",
		m.st.Key.Render("u") + "      upgrade cwd (current shell, not selected)",
		m.st.Key.Render("4") + "      open Notes for this project (local only)",
	}
	return m.st.Pane.Width(width - 2).Height(height - 2).Render(strings.Join(lines, "\n"))
}

// projectHost returns the canonical host label for `p` — "local" when
// the project lives on this machine, the remote host name otherwise.
// Centralized so the renderer and the attach router can't drift.
func projectHost(p project.Project) string {
	if p.Host == "" {
		return "local"
	}
	return p.Host
}

// scaffoldAndStartCmd runs scaffold + StartSession and returns a
// projectSessionReadyMsg with the session name so app.go can dispatch the
// tmux-attach via tea.ExecProcess.
//
// For a remote host pick, ccmux POSTs /v1/projects to that host's
// ccmuxd (so the scaffold + tmux + initial-prompt all run natively on
// the remote, no SSH-through-git-init kludge) and then fires
// remoteSessionStartedMsg so the app exec's into ssh-attach.
func scaffoldAndStartCmd(submit newProjectSubmitMsg) tea.Cmd {
	return func() tea.Msg {
		// Remote case: hand off to the remote daemon.
		if submit.Host != "" && submit.Host != "local" && submit.Address != "" {
			cli := daemon.RemoteClient(submit.Address)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			res, err := cli.NewProject(ctx, daemon.NewProjectRequest{
				Name:        submit.Name,
				Description: submit.Description,
			})
			if err != nil {
				return toastMsg{
					Text:  "new project on " + submit.Host + ": " + err.Error(),
					Kind:  toastError,
					Until: time.Now().Add(8 * time.Second),
				}
			}
			dial := submit.DialHost
			if dial == "" {
				dial = submit.Host
			}
			return remoteSessionStartedMsg{
				SessionName: res.Session,
				DialHost:    dial,
			}
		}
		// Local case (unchanged).
		opts := scaffold.Options{Name: submit.Name, Description: submit.Description}
		session, err := scaffold.StartSession(context.Background(), opts)
		if err != nil {
			return toastMsg{Text: "new project: " + err.Error(), Kind: toastError, Until: time.Now().Add(6 * time.Second)}
		}
		return projectSessionReadyMsg{Session: session, Project: submit.Name}
	}
}

// upgradeCwdCmd injects the ccmux structure into the cwd (no .git changes,
// no Claude session started). The toast surfaces what actually changed
// — without that, "upgrade" on an already-scaffolded project looked
// identical to a no-op bug.
func upgradeCwdCmd() tea.Cmd {
	return func() tea.Msg {
		cwd, err := os.Getwd()
		if err != nil {
			return toastMsg{Text: "upgrade: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
		opts := scaffold.Options{Name: filepath.Base(cwd), Dir: cwd, SkipGit: true}
		res, err := scaffold.Scaffold(&opts)
		if err != nil {
			return toastMsg{Text: "upgrade: " + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
		if !res.Changed() {
			return toastMsg{Text: cwd + ": already up to date", Kind: toastInfo, Until: time.Now().Add(4 * time.Second)}
		}
		return toastMsg{Text: "upgraded " + cwd + " — " + res.Summary(), Kind: toastSuccess, Until: time.Now().Add(5 * time.Second)}
	}
}

// textInputBlink is a small wrapper around textinput.Blink so we don't have
// to import textinput in app.go.
func textInputBlink() tea.Cmd {
	// imported indirectly; the new-project form uses textinput which already
	// owns its blink scheduling. This is here for symmetry / future use.
	return nil
}

func isNarrow(width int) bool { return width < 80 }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
