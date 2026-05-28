package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// previewMessageLimit is the cap the spec asks for: the overlay shows
// the most recent ~30 messages so the user gets enough context to
// decide whether to resume without ccmux trying to render an arbitrarily
// large transcript in a modal.
const previewMessageLimit = 30

// conversationPreviewOverlay is the `p`-key transcript preview modal.
// Parallels the Dashboard's `u` usage overlay — owned by the App, lives
// outside the screen body, swallows keystrokes except its own close
// keys. The model is intentionally small: state is just the in-focus
// conversation + the latest-loaded messages + a sticky error string.
type conversationPreviewOverlay struct {
	open         bool
	conversation conversations.Conversation
	messages     []conversations.Message
	loadErr      string
}

// Open arms the overlay against a specific conversation. The caller is
// expected to fire a load command in parallel; until SetMessages /
// SetLoadErr lands the View renders a "(loading…)" placeholder.
func (o *conversationPreviewOverlay) Open(c conversations.Conversation) {
	o.open = true
	o.conversation = c
	o.messages = nil
	o.loadErr = ""
}

// Close dismisses the overlay and drops the cached messages so the
// next Open starts clean.
func (o *conversationPreviewOverlay) Close() {
	o.open = false
	o.conversation = conversations.Conversation{}
	o.messages = nil
	o.loadErr = ""
}

// IsOpen reports whether the overlay is currently visible.
func (o conversationPreviewOverlay) IsOpen() bool { return o.open }

// Conversation returns the conversation the overlay is currently
// targeting. Empty Conversation when closed.
func (o conversationPreviewOverlay) Conversation() conversations.Conversation {
	return o.conversation
}

// SetMessages stores the loaded messages for the currently-armed
// conversation. The caller (App) is expected to drop the result when
// the user closed the overlay between Open and the load returning.
func (o *conversationPreviewOverlay) SetMessages(id string, msgs []conversations.Message) {
	if !o.open || o.conversation.ID != id {
		return
	}
	o.messages = msgs
	o.loadErr = ""
}

// SetLoadErr records a transcript-read failure so the overlay can
// surface it instead of pretending no messages exist.
func (o *conversationPreviewOverlay) SetLoadErr(id, err string) {
	if !o.open || o.conversation.ID != id {
		return
	}
	o.loadErr = err
	o.messages = nil
}

// View renders the overlay centered inside the app frame. Returns the
// empty string when the overlay is not open.
func (o conversationPreviewOverlay) View(st styles.Styles, width, height int) string {
	if !o.open {
		return ""
	}
	const (
		maxOverlayW = 96
		// Minimum width covers the closing-hint line at small terminals
		// so the modal doesn't degrade to a single illegible column.
		minOverlayW = 40
	)
	overlayW := minInt(maxOverlayW, width-st.Spacing.LG*2)
	if overlayW < minOverlayW {
		overlayW = minOverlayW
	}

	heading := st.AgentAccent(o.conversation.Agent).Bold(true).
		Render(fmt.Sprintf("%s · transcript preview", o.conversation.Agent))
	sub := st.Subtitle.Render(fmt.Sprintf("Last %d messages from %s",
		previewMessageLimit, emptyOr(o.conversation.Project, "(unknown project)")))

	body := o.renderBody(st, overlayW)

	footer := st.Muted.Render("press p or esc to close")

	parts := []string{heading, sub, "", body, "", footer}
	modal := st.PaneFocused.Width(overlayW).Render(strings.Join(parts, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func (o conversationPreviewOverlay) renderBody(st styles.Styles, overlayW int) string {
	if o.loadErr != "" {
		return st.StatusError.Render("⚠ " + o.loadErr)
	}
	if o.messages == nil {
		return st.Muted.Render("(loading recent messages…)")
	}
	if len(o.messages) == 0 {
		if o.conversation.Agent == agent.IDAntigravity {
			return st.Muted.Render("Antigravity protobuf transcripts are opaque — preview is unavailable for this conversation.")
		}
		return st.Muted.Render("No messages found in this transcript.")
	}

	// PaneFocused border + horizontal padding eats four cells; leave a
	// little extra so glamour's hard-wrap doesn't bump the border.
	wrap := overlayW - st.Spacing.LG*2
	if wrap < 30 {
		wrap = 30
	}

	md := previewMarkdown(o.messages)
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(wrap),
	)
	if err != nil {
		return md
	}
	out, rerr := renderer.Render(md)
	if rerr != nil {
		return md
	}
	return strings.TrimRight(out, "\n")
}

// previewMarkdown turns the loaded messages into a single markdown
// document. Each turn becomes a heading + body block so glamour can
// distinguish user from assistant turns visually.
func previewMarkdown(msgs []conversations.Message) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		role := strings.ToLower(m.Role)
		switch role {
		case "user":
			b.WriteString("### You\n\n")
		case "assistant":
			b.WriteString("### Assistant\n\n")
		default:
			b.WriteString("### ")
			b.WriteString(role)
			b.WriteString("\n\n")
		}
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}
