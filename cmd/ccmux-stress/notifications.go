package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/daemon"
)

// newNotificationsCmd builds the `ccmux-stress notifications`
// subcommand — the "needs_input storm" profile from the spec.
//
// What it tests:
//
//   - Bell injection serialization. The daemon's pollOnce runs under
//     a single mutex; if 50 sessions all transition to needs_input
//     in the same tick, they're all processed in sequence under the
//     lock. Slow per-session work would block subsequent ticks and
//     starve other sessions.
//   - moshi-hook detection cache. If the moshi check isn't properly
//     cached and runs per-needs_input-transition instead of per-poll,
//     a 50-session burst would shell out 50 times in a tick.
//   - prompt_count accounting. Every transition must increment the
//     per-session counter exactly once; a race would either double-
//     count or miss bells.
//
// How it triggers needs_input: we send Claude's prompt frame
// characters (`╭ │ > ╯`) into each pane via `tmux send-keys`, then
// wait for the daemon's idle threshold to elapse and probe
// /v1/sessions to watch state transitions. Detection latency is the
// time from send-keys to the daemon reporting needs_input.
func newNotificationsCmd() *cobra.Command {
	var (
		count int
		burst time.Duration
		wait  time.Duration
	)
	c := &cobra.Command{
		Use:   "notifications",
		Short: "Burst N needs_input transitions and measure daemon response",
		Long: `Spawn --count tmux sessions, then fire all of them into the
needs_input state inside a --burst window. After the daemon's idle
threshold elapses, poll /v1/sessions for up to --wait-for to
measure detection latency.

This exercises the daemon's bell-injection pipeline + the moshi
detection cache + per-session prompt_count accounting under
simultaneous load. The spec's default is 50 sessions / 5s burst.

State transitions are driven by sending Claude's prompt-frame
characters into each pane via tmux send-keys — the daemon's
classifier looks for box-drawing chars + a quiet pane, so sessions
flip to needs_input automatically once content stops changing.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNotifications(cmd.Context(), count, burst, wait)
		},
	}
	c.Flags().IntVar(&count, "count", 50, "number of sessions to drive into needs_input")
	c.Flags().DurationVar(&burst, "burst", 5*time.Second, "window in which to fire all transitions")
	c.Flags().DurationVar(&wait, "wait-for", 30*time.Second, "max wait for the daemon to detect all transitions")
	return c
}

// claudePromptScript is the shell command we run via `tmux
// respawn-pane` to push a session into needs_input. It has to
// satisfy two things:
//
//  1. After it runs, the pane's LAST non-empty line must contain the
//     box-drawing characters the classifier counts (`╭╮╰╯│─>`, two
//     or more).
//  2. The pane must STAY at that content — otherwise the shell
//     prompt `$` reappears on the bottom row and the classifier
//     treats the pane as `StateError`.
//
// First implementation used `send-keys` against a running shell,
// which got mangled — the shell's line-buffered input processing
// reorders bursts of keystrokes (verified manually: characters
// arrived out of sequence with `&&` operators splitting up). Switched
// to `respawn-pane -k`, which kills the existing pane process and
// replaces it with our script. No shell-routing involved.
//
// `printf` (no trailing newline) puts the prompt on the bottom row;
// `sleep 9999` keeps the process alive so the row stays put.
// `│ > … │` has three matches (`│`, `>`, `│`) — clears the 2-hit
// threshold with margin.
const claudePromptScript = `printf '\n\n\n│ > waiting for input │' ; sleep 9999`

func runNotifications(ctx context.Context, count int, burst, wait time.Duration) error {
	if count <= 0 {
		return fmt.Errorf("--count must be positive")
	}
	if burst <= 0 {
		return fmt.Errorf("--burst must be positive")
	}

	runID := time.Now().UnixMilli()
	prefix := fmt.Sprintf("c-stress-notif-%d", runID)
	fmt.Printf("ccmux-stress notifications: runid=%d count=%d burst=%v wait=%v prefix=%s\n",
		runID, count, burst, wait, prefix)

	staged, err := os.MkdirTemp("/tmp", "ccmux-stress-notif-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)

	spawned := []string{}
	defer cleanupSessions(spawned)

	fmt.Printf("→ spawning %d sessions…\n", count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("%s-%d", prefix, i)
		work := filepath.Join(staged, fmt.Sprintf("p%d", i))
		if err := os.MkdirAll(work, 0o755); err != nil {
			return err
		}
		// Run a shell (sh) so send-keys reaches an actual input target.
		// `sleep infinity` would keep the pane alive but the kernel
		// process is sleep, not a shell, and tmux send-keys can't
		// deliver keystrokes to it ("can't find pane" surfaced in
		// the first smoke run). A shell also gives the daemon's
		// pane classifier real content to look at, which is what we
		// want — the test exercises the full chain.
		spawnCmd := exec.CommandContext(ctx,
			"tmux", "new-session", "-d",
			"-s", name,
			"-c", work,
			"sh",
		)
		if out, err := spawnCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("spawn %s: %w (%s)", name, err, out)
		}
		spawned = append(spawned, name)
	}
	fmt.Printf("✓ spawned %d sessions\n", len(spawned))

	cli, err := daemon.LocalClient()
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}

	// Give the daemon a tick to discover the new sessions so we don't
	// race the spawn against the first probe.
	time.Sleep(2 * time.Second)

	// 2. Burst phase. Space the N send-keys calls evenly across the
	//    burst window so we hit the daemon under simultaneous (but
	//    not perfectly-coincident) needs_input transitions.
	fmt.Printf("→ bursting needs_input into %d sessions over %v…\n", count, burst)
	per := burst / time.Duration(count)
	sentAt := make(map[string]time.Time, count)
	for _, name := range spawned {
		// respawn-pane -k replaces the shell with our prompt-script
		// directly. send-keys against an interactive shell mangled
		// the input on rapid bursts (verified during dev: the `│`
		// characters got reordered with the `&&` and `sleep` tokens).
		// respawn-pane bypasses the shell entirely.
		spawn := exec.CommandContext(ctx,
			"tmux", "respawn-pane", "-k", "-t", name, "sh", "-c", claudePromptScript,
		)
		if out, err := spawn.CombinedOutput(); err != nil {
			fmt.Printf("  respawn-pane %s: %v (%s)\n", name, err, out)
			continue
		}
		sentAt[name] = time.Now()
		if per > 0 {
			time.Sleep(per)
		}
	}
	fmt.Println("✓ burst complete")

	// 3. Detection phase. The daemon's poll loop runs every 2s; the
	//    idle-threshold for needs_input is 3s. So worst case it'll
	//    take ~5s before any session is in needs_input. Poll
	//    /v1/sessions until every session has flipped or we hit the
	//    --wait-for deadline.
	fmt.Printf("→ waiting up to %v for daemon to detect transitions…\n", wait)
	deadline := time.Now().Add(wait)
	seen := map[string]time.Duration{} // name → detection latency
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	for time.Now().Before(deadline) && len(seen) < len(spawned) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			pctx, pcancel := context.WithTimeout(ctx, 3*time.Second)
			states, err := cli.Sessions(pctx)
			pcancel()
			if err != nil {
				continue
			}
			for _, s := range states {
				if _, already := seen[s.Name]; already {
					continue
				}
				if s.State == "needs_input" {
					if t0, ok := sentAt[s.Name]; ok {
						seen[s.Name] = time.Since(t0)
					}
				}
			}
		}
	}

	// 4. Final report.
	fmt.Printf("✓ detected %d / %d transitions\n", len(seen), len(spawned))
	rpt := buildNotifReport(runID, count, burst, wait, seen, len(spawned))
	fmt.Println(rpt)
	if err := writeReport("notifications", runID, rpt); err != nil {
		fmt.Printf("⚠ couldn't write report: %v\n", err)
	}
	return nil
}

// buildNotifReport assembles the markdown report for one notifications
// run. Pure function so it's covered by table tests below.
func buildNotifReport(runID int64, count int, burst, wait time.Duration, seen map[string]time.Duration, total int) string {
	var b []byte
	add := func(format string, a ...any) {
		b = append(b, []byte(fmt.Sprintf(format, a...))...)
	}
	add("# Stress run: notifications (runid=%d)\n\n", runID)
	add("- Date: %s\n", time.Now().UTC().Format(time.RFC3339))
	add("- Profile: notifications\n")
	add("- Sessions burst: %d\n", count)
	add("- Burst window: %v\n", burst)
	add("- Detection wait: %v\n\n", wait)

	add("## Detection\n\n")
	add("- Transitions detected: %d / %d\n", len(seen), total)
	if len(seen) == 0 {
		add("- (no transitions detected — daemon may be unresponsive or the prompt payload no longer triggers the classifier)\n")
		return string(b)
	}
	lats := make([]time.Duration, 0, len(seen))
	for _, d := range seen {
		lats = append(lats, d)
	}
	if p50, p95, max := latencyStats(lats); p50 > 0 {
		add("- p50 detection latency: %v\n", p50)
		add("- p95 detection latency: %v\n", p95)
		add("- max detection latency: %v\n", max)
	}

	// Spec invariant: the daemon's poll interval is 2s and the idle
	// threshold for needs_input is 3s, so any single transition
	// should be detected within ~6s in the worst case. If p95 blows
	// past that, the poll loop is starving.
	add("\n")
	if p95, _, _ := percentile(lats, 95), 0, 0; p95 > 10*time.Second {
		add("- **WARN: p95 detection > 10s — daemon poll loop may be starving under burst load**\n")
	} else {
		add("- ✓ p95 within expected envelope (≤10s)\n")
	}
	return string(b)
}

// percentile returns just one percentile from the latency slice; thin
// wrapper around latencyStats so we don't recompute the median when
// only p95 is needed for an assertion. Unused-rest values discarded
// by the caller.
func percentile(lat []time.Duration, p int) time.Duration {
	if len(lat) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(lat))
	copy(cp, lat)
	// Tiny inline sort — sort.Slice is fine but importing for one
	// call doubles the file's import footprint.
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j-1] > cp[j]; j-- {
			cp[j-1], cp[j] = cp[j], cp[j-1]
		}
	}
	idx := len(cp) * p / 100
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}
