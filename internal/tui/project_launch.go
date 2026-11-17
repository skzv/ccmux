package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
)

// launchCmdForProject and launchCmdForProjectPath are the canonical
// places to resolve the tmux launch command for a new project session.
// Both go through agent.LaunchCmd(..., false, ...) — `false` because
// every caller creates a session the user asked for as new: the
// project menu's "Start a new session" row, and the direct create for
// a project with no sessions and no history. Resuming an existing
// conversation is a separate menu row that goes through
// resumeConversationCmd with the conversation's own ID, so a
// `--continue` here would only ever pick up a conversation the user
// did not choose.
//
// Two flavors because some sites have the Project in hand and others
// only have the path. Both must agree so a project whose sidecar
// says Antigravity launches `agy` no matter which code path the user
// took. Keep a shell open if the agent fails so its startup error
// remains visible and the user can repair the environment.
func launchCmdForProject(p project.Project) string {
	return launchCmdForProjectWithCommands(p, agent.Commands{})
}

func launchCmdForProjectWithCommands(p project.Project, commands agent.Commands) string {
	return agent.LaunchCmd(p.Agent, false, commands) + " || zsh || bash || sh"
}

func launchCmdForProjectPath(projectPath string) string {
	return launchCmdForProjectPathWithCommands(projectPath, agent.Commands{})
}

func launchCmdForProjectPathWithCommands(projectPath string, commands agent.Commands) string {
	return launchCmdForProjectWithCommands(project.Project{Agent: project.ReadAgent(projectPath)}, commands)
}

// attachOrCreateForSelectedProject is Enter on Projects screen.
// Routes by Host:
//   - local (Host == "" or "local"): existing flow — tmux.New here,
//     localAttachCmd attaches.
//   - remote: POST /v1/sessions to that host's ccmuxd so the tmux
//     session is created on the remote, then ssh-attach into it
//     using the same dial path the Sessions screen uses.
func (a App) attachOrCreateForSelectedProject() (App, tea.Cmd) {
	sel := a.projectsM.Selected()
	if sel == nil {
		return a, nil
	}
	p := *sel
	host := projectHost(p)
	// Flip the loading overlay up front — both the local and remote
	// paths run a tmux.List + conversations walk that can take a
	// noticeable beat (especially when ~/.claude/projects has many
	// transcripts), and without the overlay the user sees the
	// Projects screen sitting there unresponsive after Enter.
	tick := a.startAttaching(attachKindOpening, p.Name)
	if host == "local" {
		return a, tea.Batch(tick, a.attachOrCreateLocal(p))
	}
	return a, tea.Batch(tick, a.attachOrCreateRemote(p, host))
}

// attachOrCreateLocal handles Enter on a local project. It gathers the
// project's running tmux sessions and past conversations and emits a
// projectMenuMsg so the App opens the project menu modal — the user
// then attaches, resumes, or starts a new session. When the project has
// neither (a brand-new project, never run) it skips the modal and
// creates+attaches in one step, since a one-item menu is pointless.
func (a App) attachOrCreateLocal(p project.Project) tea.Cmd {
	label := p.Name
	path := p.Path
	// Resolve the launch command from the project's sidecar (.ccmux/
	// agent) up front — this used to hardcode the Claude command,
	// which silently overrode Codex / Antigravity projects.
	launch := launchCmdForProjectWithCommands(p, a.cfg.AgentCommands())
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		var sessions []tmux.Session
		all, err := projectTmuxList(ctx)
		if err == nil {
			for _, s := range all {
				if tmux.SamePath(s.Path, path) {
					sessions = append(sessions, s)
				}
			}
		}
		convs := a.conversationsForProject(path)

		if len(sessions) == 0 && len(convs) == 0 {
			// Nothing to choose between — create and attach directly.
			//
			// Fresh context for the create: conversationsForProject above
			// walks the whole transcript tree (~/.claude, ~/.codex, …)
			// synchronously and ignores ctx, so on a heavy tree it can
			// burn the original 3s budget before we get here. Reusing
			// that already-elapsed ctx made tmux.New fail with "context
			// deadline exceeded" — which bit brand-new projects in
			// particular, since they always reach this branch.
			nctx, ncancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer ncancel()
			if err != nil {
				return toastMsg{Text: tr("start session: ") + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
			// The project's own session name: c-<project>, or its
			// path-tagged name when c-<project> belongs to a same-named
			// project in another directory — taking that name used to
			// fail with "duplicate session" (tmux.ProjectSessionName).
			session, _ := tmux.ProjectSessionName(all, path)
			if err := projectTmuxNew(nctx, session, path, launch); err != nil {
				return toastMsg{Text: tr("start session: ") + err.Error(), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
			}
			return projectSessionReadyMsg{Session: session, Project: label}
		}
		return projectMenuMsg{
			Project:       label,
			ProjectPath:   path,
			Sessions:      sessions,
			Conversations: convs,
		}
	}
}

// conversationsForProject returns past conversations whose recorded
// working directory matches projectPath (symlinks resolved on both
// sides), newest first (conversations.All already sorts by recency).
// Drives the project menu's "resume" rows.
// Honors the same headless-visibility default as the Conversations
// screen — automation runs would otherwise clutter the per-project
// resume picker just as much as the global list.
func (a App) conversationsForProject(projectPath string) []conversations.Conversation {
	all, err := conversations.All(conversations.Options{
		ExcludeHeadless: !a.conversationsM.showHeadless,
	})
	if err != nil {
		return nil
	}
	return conversations.ForProject(all, projectPath)
}

// attachOrCreateRemote starts (or attaches to) a Claude session on a
// remote ccmuxd, then ssh-attaches to it from this machine. Two
// network round-trips and the user gets dropped into a fully-running
// remote tmux session from a single Enter press.
func (a App) attachOrCreateRemote(p project.Project, host string) tea.Cmd {
	hs := a.lookupRemoteHost(host)
	if hs == nil {
		return func() tea.Msg {
			return toastMsg{Text: fmt.Sprintf(tr("no reachable daemon for host: %s"), hostLabel(host, a.hosts)), Kind: toastError, Until: time.Now().Add(5 * time.Second)}
		}
	}
	// Snapshot the dial/address (and the SSH addressing fields the
	// post-create attach needs — login user, sshd port, mosh
	// preference) before crossing the goroutine boundary.
	hostAddr := hs.Address
	dial := hs.DialHost
	if dial == "" {
		dial = dialAddrFor(*hs)
	}
	sshUser, sshPort, useMosh := hs.User, hs.SSHPort, hs.Mosh
	projectName := p.Name
	return tea.Sequence(
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cli := daemon.RemoteClient(hostAddr)
			// Continue stays false, as for local projects (#188): a
			// new session must not pick up the directory's most recent
			// conversation — often a headless run the user never saw.
			// The daemon attaches to the project's session instead of
			// creating one when it is already running.
			ss, err := cli.NewSession(ctx, daemon.NewSessionRequest{
				Project: projectName,
			})
			if err != nil {
				return toastMsg{Text: tr("remote start: ") + err.Error(), Kind: toastError, Until: time.Now().Add(6 * time.Second)}
			}
			return remoteSessionStartedMsg{
				SessionName: ss.Name,
				DialHost:    dial,
				User:        sshUser,
				SSHPort:     sshPort,
				Mosh:        useMosh,
			}
		},
	)
}

// lookupRemoteHost returns the remote hostStatus row matching the routing
// key set by refresh. It resolves session/project identity to the daemon
// address + ssh dial host pair. The Local row is
// skipped: its name is this machine's hostname, which a tailnet peer
// can share, and the peer's rows must still resolve to the peer.
func (a App) lookupRemoteHost(key string) *hostStatus {
	for i := range a.hosts {
		if !a.hosts[i].Local && a.hosts[i].key() == key {
			return &a.hosts[i]
		}
	}
	return nil
}

// uniqueSessionName is base when no session in sessions has that name,
// else the next unused numeric suffix ("c-myproject-2", "c-myproject-3",
// …). Falls back to a millisecond timestamp suffix if the first 99
// candidates are all taken.
func uniqueSessionName(sessions []tmux.Session, base string) string {
	taken := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		taken[s.Name] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixMilli())
}

// The tmux calls opening a local project makes (attachOrCreateLocal,
// the project menu's "new session") — seams so tests can drive the
// session naming without a tmux server.
var (
	projectTmuxList = tmux.List
	projectTmuxNew  = tmux.New
)
