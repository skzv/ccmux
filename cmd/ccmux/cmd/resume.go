package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tmux"
)

// newResumeCmd: `ccmux resume [<id>]` is the CLI mirror of the
// Conversations screen's Enter action. With no arg, resumes the most
// recent conversation across all agents (analogous to
// `claude --continue` / `codex resume --last` / `agy --continue` —
// but agent-agnostic). With an ID, resumes that specific conversation
// by dispatching to the agent that owns it.
//
// Why this exists alongside `ccmux list-conversations`: list is the
// read side; resume is the write side. CLAUDE.md's feature-surface
// policy: every TUI feature gets a CLI hook, and "click a conversation
// row to resume" needs a scriptable equivalent for both muscle-memory
// and remote-via-ssh flows.
func newResumeCmd() *cobra.Command {
	var (
		agentFilter string
	)
	cmd := &cobra.Command{
		Use:   "resume [conversation-id]",
		Short: "Resume a past agent conversation in a new tmux session",
		Long: `Resume a past Claude / Codex / Antigravity / Cursor / pi conversation in a
fresh tmux session running the right agent with the right --resume flag.

Forms:

  ccmux resume                    # most recent conversation across all agents
  ccmux resume <id>               # specific conversation by ID
  ccmux resume --agent claude     # most recent Claude conversation
  ccmux resume --agent codex      # most recent Codex conversation
  ccmux resume --agent antigravity# most recent Antigravity conversation
  ccmux resume --agent cursor     # most recent Cursor conversation
  ccmux resume --agent pi         # most recent pi conversation
  ccmux resume --agent grok       # most recent Grok conversation

Use ` + "`ccmux list-conversations`" + ` to discover IDs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			// Always fetch the full list — when the user passes an
			// explicit ID we need to find it regardless of headless
			// status; the default-most-recent path filters below.
			list, err := conversations.All(conversations.Options{})
			if err != nil {
				return fmt.Errorf("list conversations: %w", err)
			}
			if len(list) == 0 {
				return fmt.Errorf("no past conversations found — run an agent at least once first")
			}

			var target conversations.Conversation
			if len(args) == 1 {
				target = pickByID(list, args[0])
				if target.ID == "" {
					return fmt.Errorf("no conversation with id %q (use `ccmux list-conversations` to list)", args[0])
				}
			} else {
				// Bare `ccmux resume` shouldn't drop the user into a
				// headless automation run (`claude -p`, the SDK, or
				// `codex exec`). Filter headless rows out of the
				// most-recent picker unless the user opted them back
				// in via config — they can always target a specific
				// headless run by ID.
				showHeadless := false
				if cfg, err := config.Load(); err == nil {
					showHeadless = cfg.Conversations.ShowHeadless
				}
				if !showHeadless {
					interactive := list[:0]
					for _, c := range list {
						if !c.IsHeadless() {
							interactive = append(interactive, c)
						}
					}
					list = interactive
					if len(list) == 0 {
						return fmt.Errorf("no past interactive conversations found — only headless runs (claude -p / SDK, codex exec). Pass an ID, or set conversations.show_headless=true")
					}
				}
				// No explicit id: pick most-recent, optionally filtered by agent.
				if agentFilter != "" {
					want, ok := agent.ParseID(agentFilter)
					if !ok {
						return fmt.Errorf("unknown agent %q (claude, codex, antigravity, cursor, pi, grok)", agentFilter)
					}
					target = pickMostRecentByAgent(list, want)
					if target.ID == "" {
						return fmt.Errorf("no past %s conversation found", agentFilter)
					}
				} else {
					target = list[0] // already sorted by recency
				}
			}

			return resumeNow(target)
		},
	}
	cmd.Flags().StringVar(&agentFilter, "agent", "", "restrict to a specific agent (claude / codex / antigravity / cursor / pi / grok)")
	return cmd
}

// pickByID is the linear-scan lookup. With sub-hundred conversations
// the cost is negligible; if that ever changes we'll index by ID in
// the data layer.
func pickByID(list []conversations.Conversation, id string) conversations.Conversation {
	for _, c := range list {
		if c.ID == id {
			return c
		}
	}
	return conversations.Conversation{}
}

// pickMostRecentByAgent assumes the input is already sorted by
// LastActivity DESC (that's what conversations.All returns), so the
// first match for the requested agent is the most recent.
func pickMostRecentByAgent(list []conversations.Conversation, id agent.ID) conversations.Conversation {
	for _, c := range list {
		if c.Agent == id {
			return c
		}
	}
	return conversations.Conversation{}
}

// resumeNow spawns a fresh tmux session running the agent for the
// target conversation. After tmux.New returns we exec `tmux attach`
// in-foreground so the caller's shell hands off cleanly — same pattern
// the existing `ccmux attach` and `ccmux new` commands use.
func resumeNow(target conversations.Conversation) error {
	cfg, _ := config.Load()
	argv := target.ResumeArgsWithCommands(cfg.AgentCommands())
	if len(argv) == 0 {
		return fmt.Errorf("unknown agent %q — cannot resume", target.Agent)
	}
	shortID := target.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	sessionName := "c-resume-" + shortID
	// Quote-free join is safe — agent argv elements are well-known
	// flags + a UUID; no shell metacharacters. zsh fallback keeps the
	// pane alive if the agent binary went missing between list + resume.
	cmdline := joinArgs(argv) + " || zsh"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	detachOthers := false
	if err := tmux.New(ctx, sessionName, target.Project, cmdline); err != nil {
		// If the session already exists (e.g., the user already
		// resumed this ID earlier in the day), tmux.New errors. Treat
		// that as "attach to the existing one" rather than failing.
		if has, _ := tmux.Has(ctx, sessionName); !has {
			return fmt.Errorf("create tmux session: %w", err)
		}
		detachOthers = attachDetachOthers()
	}
	// Hand off to tmux attach via exec — replaces the current process
	// so when the user detaches they return to whatever shell launched
	// `ccmux resume`, not to ccmux itself. attachWithChrome applies
	// ccmux chrome first, same as `ccmux attach` and `ccmux new`.
	label := ""
	if target.Project != "" {
		label = filepath.Base(target.Project)
	}
	return attachWithChrome(sessionName, label, detachOthers)
}

// joinArgs glues an argv slice into a shell command, quoting each
// element so configured executable paths with spaces stay one token.
func joinArgs(argv []string) string {
	var b []byte
	for i, a := range argv {
		if i > 0 {
			b = append(b, ' ')
		}
		b = append(b, agent.ShellQuote(a)...)
	}
	return string(b)
}
