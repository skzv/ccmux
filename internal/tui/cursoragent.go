package tui

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/cursorconfig"
	"github.com/skzv/ccmux/internal/cursorusage"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// cursorTTL is how long a cursorusage.Summary stays cached before
// the sub-tab refreshes it on the next render. 30s is short enough
// that an active Cursor user sees their numbers move without a
// manual reload, and long enough that flipping between sub-tabs
// doesn't re-hit the SQLite open + queries every render tick.
const cursorTTL = 30 * time.Second

// cursorAgentModel is the Agents → Cursor sub-tab. Reads the local
// `~/.cursor/ai-tracking/ai-code-tracking.db` via internal/cursorusage
// and renders a Claude-shaped summary block (conversations, top
// models, AI lines this week, last activity). When Cursor isn't
// installed (the database file is absent), the sub-tab renders a
// muted "Cursor not detected" placeholder instead of an error.
//
// Loading is async: newCursorAgent kicks off the SQLite read in a
// Cmd and shows the spinner until the first result arrives. After
// that the Summary is cached for cursorTTL; the next render after
// the TTL expires schedules a fresh read.
type cursorAgentModel struct {
	st styles.Styles

	dbPath string

	summary cursorusage.Summary
	// notInstalled is true when the last Open returned
	// cursorusage.ErrNotInstalled — the Cursor app isn't on this
	// machine. We surface a placeholder, not an error.
	notInstalled bool
	// err is set when Open returned an unexpected error (e.g.,
	// corrupt DB, permission denied). Surfaced verbatim under a
	// muted heading so the user can debug.
	err string

	hooks  cursorconfig.HooksFile
	skills []cursorconfig.Skill

	loadedAt time.Time
	loading  bool
	spinner  spinner.Model
	browser  agentBrowser

	// now is a deterministic clock injection for golden tests.
	// Production renders read time.Now().
	now time.Time
}

// cursorLoadedMsg is the result of an async cursorusage.Open call.
// `err` non-nil + ErrNotInstalled flips the model to the
// empty-state placeholder; other errors render in the body.
type cursorLoadedMsg struct {
	Summary cursorusage.Summary
	Err     error
}

func newCursorAgent(st styles.Styles) cursorAgentModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = st.Muted
	m := cursorAgentModel{
		st:      st,
		spinner: sp,
		browser: newAgentBrowser(st),
	}
	m.dbPath = defaultCursorDBPath()
	m.hooks, _ = cursorconfig.ReadHooks()
	m.skills, _ = cursorconfig.ListSkills()
	m.browser.SetSections("Cursor configured", m.browserSections())
	return m
}

// defaultCursorDBPath resolves $HOME and returns the canonical Cursor
// ai-tracking database path. An unresolvable home falls back to the
// literal "~" prefix so the empty-state placeholder still renders;
// in practice $HOME is set everywhere ccmux runs.
func defaultCursorDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return cursorusage.DefaultDBPath("~")
	}
	return cursorusage.DefaultDBPath(home)
}

// Init kicks off the first load + spinner tick so the spinner
// animates while cursorusage.Open is running on its own goroutine.
func (m cursorAgentModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadCmd())
}

// loadCmd dispatches the SQLite read on a worker goroutine so the
// TUI's render loop doesn't block on disk I/O. The result lands as
// a cursorLoadedMsg the App routes back to the sub-tab.
func (m cursorAgentModel) loadCmd() tea.Cmd {
	dbPath := m.dbPath
	return func() tea.Msg {
		s, err := cursorusage.Open(dbPath)
		return cursorLoadedMsg{Summary: s, Err: err}
	}
}

// EnsureFresh re-issues loadCmd when the cached Summary is older than
// cursorTTL. Called by the parent agentsModel each time the Cursor
// sub-tab becomes active so the data doesn't go stale across tab
// flips.
func (m cursorAgentModel) EnsureFresh() (cursorAgentModel, tea.Cmd) {
	if m.loading {
		return m, nil
	}
	if m.loadedAt.IsZero() || m.clock().Sub(m.loadedAt) > cursorTTL {
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.loadCmd())
	}
	return m, nil
}

func (m cursorAgentModel) Update(msg tea.Msg) (cursorAgentModel, tea.Cmd) {
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
	}
	switch msg := msg.(type) {
	case cursorLoadedMsg:
		m.loading = false
		m.loadedAt = m.clock()
		if errors.Is(msg.Err, cursorusage.ErrNotInstalled) {
			m.notInstalled = true
			m.err = ""
			m.summary = cursorusage.Summary{}
			return m, nil
		}
		m.notInstalled = false
		if msg.Err != nil {
			m.err = msg.Err.Error()
			m.summary = cursorusage.Summary{}
			return m, nil
		}
		m.err = ""
		m.summary = msg.Summary
		return m, nil
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// SetSummary primes the model with a deterministic Summary so golden
// tests can render without running the spinner or the SQLite reader.
// Production code never calls this.
func (m *cursorAgentModel) SetSummary(s cursorusage.Summary, now time.Time) {
	m.summary = s
	m.notInstalled = false
	m.err = ""
	m.loading = false
	m.loadedAt = now
	m.now = now
}

// SetNotInstalled primes the model with the empty-state flag so
// golden tests can render the "Cursor not detected" placeholder.
func (m *cursorAgentModel) SetNotInstalled(now time.Time) {
	m.summary = cursorusage.Summary{}
	m.notInstalled = true
	m.err = ""
	m.loading = false
	m.loadedAt = now
	m.now = now
}

func (m cursorAgentModel) clock() time.Time {
	if m.now.IsZero() {
		return time.Now()
	}
	return m.now
}

// View renders the Cursor sub-tab. Layout mirrors the Claude sub-tab's
// section / sub-section indent step (panel title at column 0, section
// headings at column 2, body rows at column 4) so the two screens
// read as peers.
func (m cursorAgentModel) View(width, height int) string {
	return m.st.Pane.Width(width - 2).Height(height - 2).MaxWidth(width).Render(
		m.ViewBody(width-4, height-2))
}

// ViewBody renders the Cursor sub-tab's inner content without an
// outer Pane border so agentsModel.View can wrap the whole agent
// surface in one bordered block.
func (m cursorAgentModel) ViewBody(width, height int) string {
	st := m.st
	header := []string{st.AgentAccent(agent.IDCursor).Render("Cursor")}

	switch {
	case m.notInstalled:
		header = append(header,
			"",
			"  "+st.Muted.Render("Cursor not detected — install from cursor.com"),
		)
	case m.loading && m.loadedAt.IsZero():
		header = append(header,
			"",
			"  "+m.spinner.View()+" "+st.Muted.Render("reading ~/.cursor/ai-tracking…"),
		)
	case m.err != "":
		header = append(header,
			"",
			st.Subtitle.Render("Usage"),
			"  "+st.StatusWarning.Render("error: "+m.err),
		)
	default:
		header = append(header, m.renderUsageSection()...)
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

// renderUsageSection produces the Cursor sub-tab's "Usage" block —
// conversation count + top models + AI lines this week + last
// activity. Numbers render in the lavender headline style used
// across the Dashboard's Usage panel so visual emphasis is
// consistent.
func (m cursorAgentModel) renderUsageSection() []string {
	st := m.st
	s := m.summary
	lines := []string{
		"",
		st.Subtitle.Render("Usage"),
	}

	conv := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).
		Render(fmt.Sprintf("%d", s.Conversations))
	lines = append(lines, "  "+conv+" "+st.Muted.Render("conversations"))

	if len(s.Models) > 0 {
		lines = append(lines, "  "+st.Muted.Render("top models: ")+strings.Join(s.Models, ", "))
	} else {
		lines = append(lines, "  "+st.Muted.Render("top models: (none recorded)"))
	}

	linesAdded := lipgloss.NewStyle().Foreground(st.P.Lavender).Bold(true).
		Render(fmt.Sprintf("%d", s.AILinesLast7d))
	lines = append(lines, "  "+linesAdded+" "+st.Muted.Render("AI lines this week"))

	if !s.LastActivity.IsZero() {
		ago := humanDuration(m.clock().Sub(s.LastActivity))
		lines = append(lines, "  "+st.Muted.Render("last activity: ")+
			s.LastActivity.Local().Format("2006-01-02 15:04")+
			" "+st.Muted.Render("("+ago+" ago)"))
	} else {
		lines = append(lines, "  "+st.Muted.Render("last activity: (no activity yet)"))
	}
	return lines
}

// renderConfigSection produces the "Config files" sub-section —
// where Cursor stores its settings on disk. Useful as a pointer
// even though ccmux doesn't manage Cursor's settings directly.
func (m cursorAgentModel) renderConfigSection() []string {
	st := m.st
	home, _ := os.UserHomeDir()
	configRoot := agent.Cursor{}.ConfigRoot(home)
	transcriptsRoot := agent.Cursor{}.TranscriptsRoot(home)
	return []string{
		st.Subtitle.Render("Config files"),
		"  " + st.Muted.Render("config: ") + summarizePath(configRoot),
		"  " + st.Muted.Render("transcripts: ") + summarizePath(transcriptsRoot),
		"  " + st.Muted.Render("tracking db: ") + summarizePath(m.dbPath),
	}
}

// browserSections builds the Configured browser sections for the
// Cursor sub-tab. Cursor's globally-scoped configurables are Hooks
// (~/.cursor/hooks.json) and Skills (~/.cursor/skills-cursor/*). MCP
// servers and slash commands live per-project under each project's
// .cursor/ directory, so they're not surfaced in the global browser.
func (m cursorAgentModel) browserSections() []agentBrowserSection {
	return []agentBrowserSection{
		m.browserHooksSection(),
		m.browserSkillsSection(),
	}
}

func (m cursorAgentModel) browserHooksSection() agentBrowserSection {
	section := agentBrowserSection{Title: "Hooks", Color: m.st.P.Peach}
	if len(m.hooks.Hooks) == 0 {
		return section
	}
	events := make([]string, 0, len(m.hooks.Hooks))
	for k := range m.hooks.Hooks {
		events = append(events, k)
	}
	// Cursor event names are camelCase and don't share Claude's
	// preferred order, so plain alphabetical is the stable choice.
	sort.Strings(events)
	for _, event := range events {
		count := 0
		preview := []string{event, ""}
		for _, g := range m.hooks.Hooks[event] {
			for _, h := range g.Hooks {
				count++
				preview = append(preview, "  command: "+h.Command)
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

func (m cursorAgentModel) browserSkillsSection() agentBrowserSection {
	section := agentBrowserSection{Title: "Skills", Color: m.st.P.Mauve}
	for _, s := range m.skills {
		// Skill.Body is already loaded by cursorconfig.ListSkills.
		body := s.Body
		if body == "" {
			body = s.Description
		}
		section.Items = append(section.Items, agentBrowserItem{
			Label:    s.Name,
			Preview:  body,
			Markdown: true,
		})
	}
	return section
}
