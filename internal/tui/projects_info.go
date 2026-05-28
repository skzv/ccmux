package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// projectInfoOverlay renders the full-detail modal opened by `i` on
// the Projects screen. Companion to the dashboard's `u` usage overlay
// — same modal idiom, full sub-section grouping, dismiss on `i`/`esc`.
//
// It is a stateless render helper: all data comes from the caller
// (the focused project + the live session list so we can count recent
// sessions for that project). Filesystem reads (CLAUDE.md head,
// agent-sidecar file, mtime) happen inline on render — they're tiny
// I/Os bounded by the overlay's read budget (10 lines for CLAUDE.md).
type projectInfoOverlay struct{}

// claudeMdHeadLines is the number of CLAUDE.md lines the overlay
// previews. Tight enough to fit the modal even on a 40-row terminal.
const claudeMdHeadLines = 10

// View renders the overlay centered in `width`x`height`. Caller is
// responsible for routing the `i`/`esc` close keys.
func (projectInfoOverlay) View(st styles.Styles, p project.Project, sessions []daemon.SessionState, width, height int) string {
	host := projectHost(p)
	agentDisplay := agent.ByID(p.Agent).DisplayName()

	// Modal sizing. Pane has a 1-cell border on each side plus the
	// design-system horizontal padding (s.Spacing.SM = 1), so subtract
	// 4 cells to get the content area. File-preview blocks render
	// inside that area at a further 2-cell indent (s.Spacing.MD) so
	// wrapped lines and blank lines all stay aligned with their
	// "  " prefixed siblings above.
	modalW := minInt(96, width-4)
	contentW := modalW - 4
	if contentW < 10 {
		contentW = 10
	}
	indentedBlock := lipgloss.NewStyle().
		PaddingLeft(st.Spacing.MD).
		Width(contentW)

	lines := []string{
		st.Emphasis.Render(p.Name) + "   " + st.HostColor(host).Render("● "+host),
		st.Muted.Render(p.Path),
		"",
	}

	lines = append(lines, st.Subtitle.Render("Identity"))
	lines = append(lines, fmt.Sprintf("  session   %s", st.Emphasis.Render(p.SessionName())))
	lines = append(lines, fmt.Sprintf("  agent     %s", st.Emphasis.Render(agentDisplay)))
	detected := renderScaffoldChips(st, p, false)
	if detected == "" {
		lines = append(lines, "  detected  "+st.Muted.Render("(none)"))
	} else {
		lines = append(lines, "  detected  "+strings.TrimLeft(detected, " "))
	}
	if !p.Modified.IsZero() {
		lines = append(lines, fmt.Sprintf("  modified  %s", st.Muted.Render(humanModified(p.Modified))))
	}
	lines = append(lines, "")

	// Recent-sessions count: how many tmux sessions on the same host
	// claim this project's session name as a prefix. Cheap O(n) scan of
	// the already-loaded live session list.
	count := countSessionsForProject(p, sessions)
	lines = append(lines, st.Subtitle.Render("Sessions"))
	if count == 0 {
		lines = append(lines, "  "+st.Muted.Render("no active sessions"))
	} else {
		lines = append(lines, fmt.Sprintf("  %s active", st.Emphasis.Render(fmt.Sprintf("%d", count))))
	}
	lines = append(lines, "")

	// Agent sidecar dump: the raw .ccmux/agent file. Skipped silently
	// when the project doesn't have one (back-compat path).
	if host == "local" {
		if sidecar := readAgentSidecar(p.Path); sidecar != "" {
			lines = append(lines, st.Subtitle.Render("Agent sidecar"))
			lines = append(lines, indentedBlock.Render(st.Muted.Render(".ccmux/agent")))
			lines = append(lines, indentedBlock.Render(strings.TrimRight(sidecar, "\n")))
			lines = append(lines, "")
		}
	}

	// CLAUDE.md / AGENTS.md previews. Skipped when the marker is
	// absent or the file is unreadable (e.g., remote project). Each
	// block is rendered through indentedBlock so word-wrapped
	// continuations and blank lines both keep the 2-cell indent.
	if host == "local" {
		if p.HasCM {
			if head := readMarkdownHead(p.Path, "CLAUDE.md", claudeMdHeadLines); head != "" {
				lines = append(lines, st.Subtitle.Render(fmt.Sprintf("CLAUDE.md (first %d lines)", claudeMdHeadLines)))
				lines = append(lines, indentedBlock.Render(head))
				lines = append(lines, "")
			}
		}
		if p.HasAgents {
			if head := readMarkdownHead(p.Path, "AGENTS.md", claudeMdHeadLines); head != "" {
				lines = append(lines, st.Subtitle.Render(fmt.Sprintf("AGENTS.md (first %d lines)", claudeMdHeadLines)))
				lines = append(lines, indentedBlock.Render(head))
				lines = append(lines, "")
			}
		}
	}

	lines = append(lines, st.Muted.Render("press i or esc to close"))

	body := strings.Join(lines, "\n")
	modal := st.PaneFocused.Width(modalW).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// countSessionsForProject counts live sessions whose name starts with
// the project's canonical session name. We do a prefix match (not
// equality) so the dashboard's multi-session-per-project convention
// (e.g. `c-ccmux`, `c-ccmux-2`) all contribute.
func countSessionsForProject(p project.Project, sessions []daemon.SessionState) int {
	name := p.SessionName()
	host := projectHost(p)
	n := 0
	for _, s := range sessions {
		sHost := s.Host
		if sHost == "" {
			sHost = "local"
		}
		if sHost != host {
			continue
		}
		if s.Name == name || strings.HasPrefix(s.Name, name+"-") {
			n++
		}
	}
	return n
}

// readAgentSidecar returns the raw contents of `<project>/.ccmux/agent`
// or "" if the file isn't there / unreadable. Sub-1KB by construction.
func readAgentSidecar(projectPath string) string {
	b, err := os.ReadFile(filepath.Join(projectPath, ".ccmux", "agent"))
	if err != nil {
		return ""
	}
	return string(b)
}

// readMarkdownHead returns the first `n` lines of `<project>/<name>`
// joined with "\n", or "" when the file is missing or unreadable.
// Used for both the CLAUDE.md and AGENTS.md previews — the readers
// were identical so they collapse to one.
func readMarkdownHead(projectPath, name string, n int) string {
	f, err := os.Open(filepath.Join(projectPath, name))
	if err != nil {
		return ""
	}
	defer f.Close()
	out := make([]string, 0, n)
	sc := bufio.NewScanner(f)
	for sc.Scan() && len(out) < n {
		out = append(out, sc.Text())
	}
	return strings.Join(out, "\n")
}

// humanModified formats `t` as an absolute date when older than a
// week, otherwise as a relative bucket. Mirrors the Sessions row age
// format for consistency.
func humanModified(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	if d < 7*24*time.Hour {
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}
