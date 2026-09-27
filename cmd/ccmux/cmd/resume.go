package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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
		Long: `Resume a past agent conversation in a fresh tmux session running the
right agent with its native resume command.

Forms:

  ccmux resume                    # most recent conversation across all agents
  ccmux resume <id>               # specific conversation by ID (or a unique prefix)
  ccmux resume --agent <agent>    # most recent conversation for one agent

Agents: ` + agentIDList() + `.

Use ` + "`ccmux list-conversations`" + ` to discover IDs. The shortened IDs in
its table work as-is, trailing "…" included.`,
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
				if target, err = pickByID(list, args[0]); err != nil {
					return err
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
						return fmt.Errorf("unknown agent %q (want %s)", agentFilter, agentIDList())
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
	cmd.Flags().StringVar(&agentFilter, "agent", "", "restrict to a specific agent: "+agentIDList())
	return cmd
}

// pickByID resolves the ID a user typed or pasted. An exact match
// wins; otherwise a unique prefix does, because the list-conversations
// table shows IDs cut to 11 characters plus "…" — pasting one used to
// fail with "no conversation with id". A trailing "…" (or "...") from
// that column is stripped first. A prefix shared by several
// conversations is an error that lists them, never a guess.
//
// Linear scan: with sub-hundred conversations the cost is negligible;
// if that ever changes we'll index by ID in the data layer.
func pickByID(list []conversations.Conversation, id string) (conversations.Conversation, error) {
	id = strings.TrimSpace(id)
	for _, c := range list {
		if c.ID == id {
			return c, nil
		}
	}
	prefix := strings.TrimSuffix(strings.TrimSuffix(id, "…"), "...")
	var matches []conversations.Conversation
	if prefix != "" {
		for _, c := range list {
			if strings.HasPrefix(c.ID, prefix) {
				matches = append(matches, c)
			}
		}
	}
	switch len(matches) {
	case 0:
		return conversations.Conversation{}, fmt.Errorf("no conversation with id %q (use `ccmux list-conversations` to list)", id)
	case 1:
		return matches[0], nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "conversation id %q is ambiguous — it matches %d conversations:", id, len(matches))
	for _, c := range matches {
		fmt.Fprintf(&b, "\n  %s  %s", c.ID, c.Agent)
		if c.Preview != "" {
			fmt.Fprintf(&b, "  %q", truncateRunes(c.Preview, 50))
		}
	}
	b.WriteString("\npass more of the id (`ccmux list-conversations --json` prints full ids)")
	return conversations.Conversation{}, errors.New(b.String())
}

// truncateRunes shortens s to at most n runes, marking a cut with "…".
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
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
	if err := target.ValidateResume(); err != nil {
		return err
	}
	cfg, _ := config.Load()
	argv := target.ResumeArgsWithCommands(cfg.AgentCommands())
	if len(argv) == 0 {
		return fmt.Errorf("unknown agent %q — cannot resume", target.Agent)
	}
	// Quote-free join is safe — agent argv elements are well-known
	// flags + a UUID; no shell metacharacters. zsh fallback keeps the
	// pane alive if the agent binary went missing between list + resume.
	cmdline := joinArgs(argv) + " || zsh"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessionName, existed, err := ensureResumeSession(ctx, target, cmdline)
	if err != nil {
		return err
	}
	detachOthers := false
	if existed {
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

// ensureResumeSession creates the tmux session that resumes target —
// named by conversations.ResumeSessionName, the same helper the TUI
// uses — or, when that session already exists (the conversation was
// resumed earlier and is still running), reports existed=true so the
// caller attaches to it instead of failing.
func ensureResumeSession(ctx context.Context, target conversations.Conversation, cmdline string) (name string, existed bool, err error) {
	name = conversations.ResumeSessionName(target.ID)
	if err := resumeTmuxNew(ctx, name, target.Project, cmdline); err != nil {
		if has, _ := resumeTmuxHas(ctx, name); !has {
			return "", false, fmt.Errorf("create tmux session: %w", err)
		}
		existed = true
	}
	if err := resumeTmuxSetAgent(ctx, name, string(target.Agent)); err != nil {
		// Don't leave an untagged session behind that the next resume
		// would silently reuse — but only kill one this call created
		// (as the TUI does), never a session that was already running.
		if !existed {
			_ = resumeTmuxKill(ctx, name)
		}
		return "", false, fmt.Errorf("tag tmux session %s with its agent: %w", name, err)
	}
	return name, existed, nil
}

// The tmux calls ensureResumeSession makes — package-level seams so
// tests can drive the create / already-exists paths without a tmux
// server.
var (
	resumeTmuxNew      = tmux.New
	resumeTmuxHas      = tmux.Has
	resumeTmuxSetAgent = tmux.SetSessionAgent
	resumeTmuxKill     = tmux.Kill
)

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
