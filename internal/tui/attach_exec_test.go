package tui

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// runCaptured runs a stand-in for an attach command the way execAttach
// does (stderr captured), without a Bubble Tea program.
func runCaptured(t *testing.T, script, label string, shell bool) attachExitedMsg {
	t.Helper()
	c := exec.CommandContext(context.Background(), "sh", "-c", script)
	tail := captureStderr(c)
	return attachExit(c.Run(), label, tail, nil, shell)
}

// lastToast returns the toast the attachExitedMsg handler queued — the
// last command of its batch — without running the refreshes before it.
func lastToast(t *testing.T, cmd tea.Cmd) toastMsg {
	t.Helper()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("attach exit produced %T, want a batch ending in a toast", batch)
	}
	toast, ok := batch[len(batch)-1]().(toastMsg)
	if !ok {
		t.Fatal("attach exit queued no toast")
	}
	return toast
}

// TestAttachFailure_ToastNamesCommandAndError — every attach/ssh/mosh
// failure toasted "tmux: exit status 1": the command that failed wasn't
// named and what it printed (tmux's "can't find session: …") was wiped
// by the TUI's redraw. The toast must carry both.
func TestAttachFailure_ToastNamesCommandAndError(t *testing.T) {
	msg := runCaptured(t, `echo "can't find session: c-gone" >&2; exit 1`, "tmux attach -t c-gone", false)
	if msg.Err == nil {
		t.Fatal("a failing attach reported success")
	}
	a := newAppForTest(t)
	_, cmd := a.Update(msg)
	toast := lastToast(t, cmd)
	for _, want := range []string{"tmux attach -t c-gone", "can't find session: c-gone", "no longer exists"} {
		if !strings.Contains(toast.Text, want) {
			t.Errorf("toast %q lacks %q", toast.Text, want)
		}
	}
	if strings.Contains(toast.Text, "tmux: exit status") {
		t.Errorf("toast is still the bare exit status: %q", toast.Text)
	}

	// Silent failure: the exit error is all there is to say.
	msg = runCaptured(t, `exit 3`, "mosh mini", false)
	if got := attachFailureText(msg); got != "mosh mini: exit status 3" {
		t.Errorf("silent failure toast = %q", got)
	}
}

// TestNetworkShell_RemoteExitStatusIsNotAFailure — the Network screen's
// ssh opens an interactive shell whose exit status is the remote
// shell's; leaving after a failed command toasted an error. Only ssh's
// own 255 (couldn't connect / auth) is a failure — with its stderr kept
// for the SSH-wizard auto-route.
func TestNetworkShell_RemoteExitStatusIsNotAFailure(t *testing.T) {
	if msg := runCaptured(t, `exit 1`, "ssh mini", true); msg.Err != nil {
		t.Errorf("remote shell exiting 1 reported as failure: %v", msg.Err)
	}
	msg := runCaptured(t, `echo "user@mini: Permission denied (publickey)." >&2; exit 255`, "ssh mini", true)
	if msg.Err == nil {
		t.Fatal("ssh exit 255 not reported")
	}
	msg.RemoteSSHTarget = &attachRemoteTarget{Host: "mini", Port: 22}
	if remoteAttachTargetFromErr(msg) == nil {
		t.Error("captured 'Permission denied' didn't route to the SSH wizard")
	}
}

// TestToast_LongMessageEndsOnAWordAndPointsToHelp — a long toast (a
// config.toml parse error) was cut mid-word ("…expecte"). It must end on
// a whole word with an ellipsis, say where the full text is, and the
// help overlay's Recent activity must hold all of it.
func TestToast_LongMessageEndsOnAWordAndPointsToHelp(t *testing.T) {
	long := `config.toml didn't load; using defaults and not saving changes: parse config "/Users/someone/Library/Mobile Documents/com~apple~CloudDocs/dotfiles/.config/ccmux/config.toml": toml: line 7 (last key "sessions.attach_mode"): expected value but found "mirr" instead — values need quotes, as in attach_mode = "mirror"; see the configuration guide for every key and its default`
	a := newHomeApp(t, 80, 30, 0)
	a, _ = updateApp(t, a, toastMsg{Text: long, Kind: toastError})
	out := a.View()
	assertNoOverflow(t, out, 80)
	plain := strings.Split(ansi.Strip(out), "\n")
	// The line above the "full text" hint is the clipped text's last.
	var ellipsisLine string
	for i, l := range plain {
		if strings.Contains(l, "full text: press ?") && i > 0 {
			ellipsisLine = plain[i-1]
		}
	}
	if !strings.Contains(ellipsisLine, "…") {
		t.Fatalf("clipped toast doesn't end with an ellipsis and a pointer to the full text:\n%s", strings.Join(plain, "\n"))
	}
	before := strings.TrimSpace(strings.SplitN(strings.Trim(ellipsisLine, " │"), "…", 2)[0])
	words := strings.Fields(before)
	lastWord := words[len(words)-1]
	if !strings.Contains(long, " "+lastWord+" ") && !strings.HasSuffix(long, " "+lastWord) {
		t.Errorf("toast ends mid-word: %q", before)
	}

	a, _ = updateApp(t, a, keyMsg("?"))
	var seen strings.Builder
	for i := 0; i < 80; i++ {
		seen.WriteString(ansi.Strip(a.View()))
		a, _ = updateApp(t, a, keyMsg("down"))
	}
	if !strings.Contains(strings.Join(strings.Fields(seen.String()), " "), "its default") {
		t.Error("help's Recent activity never shows the end of the toast")
	}
}
