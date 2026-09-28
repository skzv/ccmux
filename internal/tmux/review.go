package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Review is the daemon's record, kept on a session itself, of what the
// user has and hasn't looked at there: whether they have reviewed its
// latest prompt, how many of its agent's turns have ended in one, and
// the state the session was in when that was written. Kept as the
// session's user options (@ccmux_seen, @ccmux_prompts, @ccmux_state),
// it outlives a daemon restart and moves with the session when it is
// renamed straight through tmux; List reads it back as Session.Review.
type Review struct {
	// Recorded is whether the session carries a record at all: a daemon
	// has written one (SetSessionReview).
	Recorded bool
	Seen     bool
	Prompts  int
	State    string
}

// The user options a Review is kept in.
const (
	seenOption    = "@ccmux_seen"
	promptsOption = "@ccmux_prompts"
	stateOption   = "@ccmux_state"
)

// SetSessionReview writes r on the session called name: its three
// options in one tmux invocation (`set-option … ; set-option …`), so a
// record is never half written. A name no target can carry is refused
// (ErrUntargetable; ErrSessionIDTarget for "$…"): tmux reads "=$1:" as
// the session whose ID is $1, so the record would land on some other
// session.
func SetSessionReview(ctx context.Context, name string, r Review) error {
	if err := checkOptionTarget(name); err != nil {
		return fmt.Errorf("record session review: %w", err)
	}
	seen := "0"
	if r.Seen {
		seen = "1"
	}
	target := exactPane(name)
	args := []string{
		"set-option", "-t", target, seenOption, seen, ";",
		"set-option", "-t", target, promptsOption, strconv.Itoa(max(r.Prompts, 0)), ";",
		"set-option", "-t", target, stateOption, r.State,
	}
	if out, err := command(ctx, "tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("record session review: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// parseReview reads a Review back from its options' values as
// list-sessions prints them ("" for an option the session doesn't
// have). Only a seen flag of "0" or "1" makes a record: anything else
// (no record, or one nobody but a daemon should have written) is none.
func parseReview(seen, prompts, state string) Review {
	var r Review
	switch strings.TrimSpace(seen) {
	case "1":
		r.Seen = true
	case "0":
	default:
		return Review{}
	}
	r.Recorded = true
	if n, err := strconv.Atoi(strings.TrimSpace(prompts)); err == nil && n > 0 {
		r.Prompts = n
	}
	r.State = strings.TrimSpace(state)
	return r
}
