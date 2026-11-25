package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
)

// newReadConversationCmd: `ccmux read-conversation <id> [--agent <id>] [--limit N] [--json]`
// — CLI mirror of the Conversations screen `p` preview overlay.
func newReadConversationCmd() *cobra.Command {
	var (
		agentFilter string
		limit       int
		asJSON      bool
	)
	c := &cobra.Command{
		Use:   "read-conversation <conversation-id>",
		Short: "Print recent messages from a past agent conversation transcript",
		Long: `Read and display recent user/assistant turns from a past conversation
transcript (discovered via ` + "`ccmux list-conversations`" + `). Accepts a full ID
or any unique prefix (trailing "…" stripped). Mirrors pressing 'p' on
the Conversations tab.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := conversations.All(conversations.Options{})
			if err != nil {
				return fmt.Errorf("list conversations: %w", err)
			}
			candidates := list
			if agentFilter != "" {
				want, ok := agent.ParseID(agentFilter)
				if !ok {
					return fmt.Errorf("unknown agent %q (want %s)", agentFilter, agentIDList())
				}
				filtered := make([]conversations.Conversation, 0, len(list))
				for _, conv := range list {
					if conv.Agent == want {
						filtered = append(filtered, conv)
					}
				}
				candidates = filtered
			}
			target, err := pickByID(candidates, args[0])
			if err != nil {
				return err
			}
			if limit <= 0 {
				limit = 30
			}
			msgs, err := conversations.RecentMessages(target, limit)
			if err != nil {
				return fmt.Errorf("read transcript %s: %w", target.ID, err)
			}
			if asJSON {
				if msgs == nil {
					msgs = []conversations.Message{}
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					ID       string                  `json:"id"`
					Agent    agent.ID                `json:"agent"`
					Project  string                  `json:"project"`
					Messages []conversations.Message `json:"messages"`
				}{
					ID: target.ID, Agent: target.Agent, Project: target.Project, Messages: msgs,
				})
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s  (%s)\n", safeField(target.ID), agent.ByID(target.Agent).DisplayName())
			if target.Project != "" {
				fmt.Fprintf(out, "project: %s\n", safeField(target.Project))
			}
			fmt.Fprintln(out, strings.Repeat("-", 60))
			if len(msgs) == 0 {
				fmt.Fprintln(out, "(no readable messages)")
				return nil
			}
			for i, m := range msgs {
				if i > 0 {
					fmt.Fprintln(out)
				}
				role := strings.ToUpper(safeField(m.Role))
				if role == "" {
					role = "MESSAGE"
				}
				if !m.Timestamp.IsZero() {
					fmt.Fprintf(out, "[%s] %s\n", m.Timestamp.Local().Format("2006-01-02 15:04"), role)
				} else {
					fmt.Fprintf(out, "%s\n", role)
				}
				fmt.Fprintln(out, safeText(m.Content))
			}
			return nil
		},
	}
	c.Flags().StringVar(&agentFilter, "agent", "", "restrict ID lookup to a specific agent: "+agentIDList())
	c.Flags().IntVarP(&limit, "limit", "n", 30, "maximum number of recent messages to print")
	c.Flags().BoolVar(&asJSON, "json", false, "output conversation metadata and messages as JSON")
	return c
}
