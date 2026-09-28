package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Pane is one pane of a session, as ListPanes reports it.
type Pane struct {
	// ID is tmux's pane id ("%12"): unique on the server and stable for
	// the pane's whole life, whatever happens to window and pane
	// indexes around it. Ids grow as panes are created, so the lowest
	// id in a session is its oldest pane.
	ID     string
	Window int // #{window_index}
	Index  int // #{pane_index} within the window
	Width  int
	Height int
	// Active marks the session's current pane: the active pane of its
	// active window, the one a bare `=name:` target resolves to.
	Active bool
	// Title is #{pane_title}, the title the program in the pane last
	// set with OSC 2 (tmux keeps it after that program exits).
	Title string
}

// paneListFormat is ListPanes' -F format. pane_title is LAST so a tab
// in a title can't shift the other columns: parsePanes splits with the
// field count and lets the final field absorb the rest.
const paneListFormat = "#{pane_id}\t#{window_index}\t#{pane_index}\t#{pane_width}\t#{pane_height}\t#{window_active}#{pane_active}\t#{pane_title}"

// ListPanes returns every pane of every window in the named session
// (exact name match), in window then pane order.
//
// One list-panes call carries everything the daemon's poll loop needs
// besides the pane body — which pane to read, its size and its title —
// so resolving the agent's pane costs no more shell-outs than the
// display-message it replaces.
func ListPanes(ctx context.Context, session string) ([]Pane, error) {
	out, err := command(ctx, "tmux", "list-panes", "-s", "-t", exactSession(session), "-F", paneListFormat).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", withStderr(err))
	}
	return parsePanes(out), nil
}

// parsePanes turns `list-panes -F paneListFormat` output into Panes,
// skipping lines that don't parse.
func parsePanes(out []byte) []Pane {
	var panes []Pane
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, "\t", 7)
		if len(parts) < 7 || !validPaneID(parts[0]) {
			continue
		}
		p := Pane{
			ID:     parts[0],
			Window: atoi(parts[1]),
			Index:  atoi(parts[2]),
			Width:  atoi(parts[3]),
			Height: atoi(parts[4]),
			Active: parts[5] == "11",
			Title:  parts[6],
		}
		panes = append(panes, p)
	}
	return panes
}

// OldestPane returns the pane with the lowest id: the one the session
// was created with, unless it has since exited. For a session ccmux
// started, that is the pane running the agent — however many windows
// or splits the user has added since (a split with -b, a swapped or
// renumbered window, a non-zero base-index all move indexes around; the
// id stays put). ok is false for no panes.
func OldestPane(panes []Pane) (Pane, bool) {
	var best Pane
	found := false
	for _, p := range panes {
		if !validPaneID(p.ID) {
			continue
		}
		if !found || paneNumber(p.ID) < paneNumber(best.ID) {
			best, found = p, true
		}
	}
	return best, found
}

// paneNumber is the numeric part of a valid pane id.
func paneNumber(id string) int { return atoi(id[1:]) }

// errBadPaneID guards CapturePaneID's target.
var errBadPaneID = errors.New("tmux: not a pane id")

// validPaneID reports whether id is a tmux pane id: "%" and digits.
func validPaneID(id string) bool {
	if len(id) < 2 || id[0] != '%' {
		return false
	}
	for _, c := range id[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// CapturePaneID is CapturePane for one specific pane, by its tmux pane
// id (Pane.ID) rather than a session's active pane. Anything but a
// pane id is refused, so the target can never be read as a session or
// window spec.
func CapturePaneID(ctx context.Context, paneID string, lines int) (string, error) {
	if !validPaneID(paneID) {
		return "", fmt.Errorf("%w: %q", errBadPaneID, paneID)
	}
	args := []string{"capture-pane", "-p", "-t", paneID}
	if lines > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", lines))
	}
	out, err := command(ctx, "tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", withStderr(err))
	}
	return string(out), nil
}

// SendKeysPane is SendKeys for one specific pane, by its tmux pane id
// (Pane.ID) rather than a session's active pane. As with
// CapturePaneID, anything but a pane id is refused, so the target can
// never be read as a session or window spec.
func SendKeysPane(ctx context.Context, paneID, keys string) error {
	if !validPaneID(paneID) {
		return fmt.Errorf("%w: %q", errBadPaneID, paneID)
	}
	// "--" as in SendKeys: keys starting with "-" are typed, not parsed.
	if out, err := command(ctx, "tmux", "send-keys", "-t", paneID, "--", keys).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
