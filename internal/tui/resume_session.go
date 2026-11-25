package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tmux"
)

func joinShellArgs(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		parts[i] = agent.ShellQuote(arg)
	}
	return strings.Join(parts, " ")
}

// shortConversationID truncates a conversation UUID to its first 8
// chars — enough to be recognizable in a tmux session name or a toast
// without the full 36-char UUID hogging the line. Short IDs that are
// already ≤8 chars pass through unchanged.
func shortConversationID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// resumeConversationCmd spawns a fresh tmux session running the agent
// that owns conversation `c`, with the agent's per-CLI --resume flag
// pointed at its ID, and emits a conversationResumedMsg the App
// handler turns into an attach + toast + sessions refresh. Shared by
// the Conversations screen's Enter handler and the project menu's
// "resume" rows.
//
// Session naming: conversations.ResumeSessionName (c-resume-<short-id>
// -<hash>) rather than the usual c-<project>, so a resume never lands
// in the project's regular session, and — shared with `ccmux resume` —
// resuming the same conversation again attaches to its existing
// session instead of failing "duplicate session".
//
// cwd: when the conversation has a known Project path, we cd there so
// the agent's working directory matches what it had originally —
// otherwise the user falls into $HOME and `/file edit some.go`
// completions break.
func (a App) resumeConversationCmd(c conversations.Conversation) tea.Cmd {
	return func() tea.Msg {
		if err := c.ValidateResume(); err != nil {
			return conversationResumedMsg{Err: err}
		}
		argv := c.ResumeArgsWithCommands(a.cfg.AgentCommands())
		if len(argv) == 0 {
			return conversationResumedMsg{Err: fmt.Errorf("don't know how to resume agent %q", c.Agent)}
		}
		sessionName := conversations.ResumeSessionName(c.ID)
		// Build the shell command tmux runs in the new pane. POSIX shell
		// fallback chain keeps the pane alive if the agent binary or zsh
		// is missing (e.g. minimal Linux hosts).
		cmdline := joinShellArgs(argv) + " || zsh || bash || sh"
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Resumed before and still running: attach to that session (as
		// `ccmux resume` does) rather than failing with tmux's
		// "duplicate session". It already carries its agent tag.
		existing := conversationResumedMsg{
			Session:  sessionName,
			Project:  c.Project,
			Agent:    string(c.Agent),
			Existing: true,
		}
		if has, _ := resumeTmuxHas(ctx, sessionName); has {
			return existing
		}
		// A new session needs the conversation's folder: tmux would
		// quietly start one whose folder is gone in $HOME, where the
		// agent can't find the conversation.
		if err := c.ValidateResumeFolder(); err != nil {
			return conversationResumedMsg{Err: err}
		}
		// The agent tag rides on the tmux call that creates the session
		// (tmux.NewWithAgent). Tagged by a second call, the session sat
		// untagged in between, and a daemon poll tick there classified
		// it with its project's agent — a resumed Codex conversation in
		// a Claude project read as a crashed Claude.
		if err := resumeTmuxNew(ctx, sessionName, c.Project, cmdline, string(c.Agent)); err != nil {
			if has, _ := resumeTmuxHas(ctx, sessionName); !has {
				return conversationResumedMsg{Err: fmt.Errorf("tmux new-session: %w", err)}
			}
			// A concurrent resume created it first: attach to it, and
			// never kill a session this call didn't create.
			if strings.Contains(err.Error(), "duplicate session") {
				return existing
			}
			// new-session ran but the set-option half of the same
			// invocation failed. Tag it now; if that fails too, don't
			// leave an untagged session behind for the next resume to
			// attach to.
			if terr := resumeTmuxSetAgent(ctx, sessionName, string(c.Agent)); terr != nil {
				_ = resumeTmuxKill(ctx, sessionName)
				return conversationResumedMsg{Err: fmt.Errorf("tag tmux session %s with its agent: %w (create: %v)", sessionName, terr, err)}
			}
		}
		return conversationResumedMsg{
			Session: sessionName,
			Project: c.Project,
			Agent:   string(c.Agent),
		}
	}
}

// The tmux calls resumeConversationCmd makes — package-level seams so
// tests can drive the create / already-exists paths without a tmux
// server.
var (
	resumeTmuxNew      = tmux.NewWithAgent
	resumeTmuxHas      = tmux.Has
	resumeTmuxSetAgent = tmux.SetSessionAgent
	resumeTmuxKill     = tmux.Kill
)
