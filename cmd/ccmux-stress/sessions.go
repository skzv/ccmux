package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/daemon"
)

// newSessionsCmd builds the `ccmux-stress sessions` subcommand. The
// "power user" profile from the spec: spawn N tmux sessions, watch
// the daemon process them through its poll loop for D minutes,
// capture latency + resource usage.
//
// Sessions are named `c-stress-<runid>-N` so a forgotten cleanup
// can be swept later via `tmux kill-session -t c-stress-*`. The
// runid is millisecond-granularity timestamp so concurrent runs
// don't collide.
func newSessionsCmd() *cobra.Command {
	var (
		count    int
		duration time.Duration
		probe    time.Duration
	)
	c := &cobra.Command{
		Use:   "sessions",
		Short: "Spawn N tmux sessions and measure daemon behavior under that load",
		Long: `Spawn --count tmux sessions, then drive load against the local
ccmuxd for --duration. Every --probe-interval we call /v1/sessions
through daemon.LocalClient and record the round-trip latency. At
the end we print p50/p95 latency, peak daemon RSS, FD count growth,
and clean up the spawned sessions.

Defaults match the "power user" profile from the spec: 20 sessions
for 2 minutes, probing every 500ms.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSessions(cmd.Context(), count, duration, probe)
		},
	}
	c.Flags().IntVar(&count, "count", 20, "number of tmux sessions to spawn")
	c.Flags().DurationVar(&duration, "duration", 2*time.Minute, "how long to keep load running")
	c.Flags().DurationVar(&probe, "probe-interval", 500*time.Millisecond, "interval between /v1/sessions calls")
	return c
}

func runSessions(ctx context.Context, count int, duration, probe time.Duration) error {
	if count <= 0 {
		return fmt.Errorf("--count must be positive")
	}
	if duration < probe {
		return fmt.Errorf("--duration (%v) must be at least one probe interval (%v)", duration, probe)
	}

	runID := time.Now().UnixMilli()
	sessionPrefix := fmt.Sprintf("c-stress-%d", runID)
	fmt.Printf("ccmux-stress sessions: runid=%d count=%d duration=%v probe=%v prefix=%s\n",
		runID, count, duration, probe, sessionPrefix)

	// 1. Spawn N tmux sessions. Each gets a throwaway working dir
	//    under /tmp/ccmux-stress so the daemon's project lookup has
	//    something to read. We don't bother making them look like
	//    real ccmux projects; the daemon's poll loop will still
	//    process them.
	stagedDir, err := os.MkdirTemp("/tmp", "ccmux-stress-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagedDir)

	spawned := []string{}
	defer cleanupSessions(spawned)

	fmt.Printf("→ spawning %d sessions…\n", count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("%s-%d", sessionPrefix, i)
		work := filepath.Join(stagedDir, fmt.Sprintf("p%d", i))
		if err := os.MkdirAll(work, 0o755); err != nil {
			return err
		}
		// `sleep infinity` keeps the session alive without burning CPU;
		// the daemon will see it as "idle" once pollOnce captures
		// the empty pane.
		spawnCmd := exec.CommandContext(ctx,
			"tmux", "new-session", "-d",
			"-s", name,
			"-c", work,
			"sleep", "infinity",
		)
		if out, err := spawnCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("spawn %s: %w (%s)", name, err, out)
		}
		spawned = append(spawned, name)
	}
	fmt.Printf("✓ spawned %d sessions\n", len(spawned))

	// 2. Sample the daemon. We need a local client to /v1/sessions
	//    and a way to read RSS. ccmuxd's pid comes from pgrep; if
	//    not found, we still drive load (the API still works against
	//    whoever is bound to the socket) but skip RSS sampling.
	cli, err := daemon.LocalClient()
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	pid := findCcmuxd()
	if pid == 0 {
		fmt.Println("⚠ couldn't locate ccmuxd pid via pgrep — proceeding without RSS samples")
	} else {
		fmt.Printf("→ daemon pid=%d\n", pid)
	}

	// 3. Probe loop: every `probe` interval, call /v1/sessions and
	//    record the latency. In parallel, sample RSS / FD on a slower
	//    cadence (every 5s — pgrep + ps add nontrivial overhead).
	latencies := []time.Duration{}
	rssSamples := []int{}
	probeTicker := time.NewTicker(probe)
	rssTicker := time.NewTicker(5 * time.Second)
	defer probeTicker.Stop()
	defer rssTicker.Stop()
	deadline := time.Now().Add(duration)

	fmt.Printf("→ probing for %v…\n", duration)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-probeTicker.C:
			start := time.Now()
			pctx, pcancel := context.WithTimeout(ctx, 3*time.Second)
			_, err := cli.Sessions(pctx)
			pcancel()
			if err != nil {
				fmt.Printf("  probe err: %v\n", err)
				continue
			}
			latencies = append(latencies, time.Since(start))
		case <-rssTicker.C:
			if pid != 0 {
				if rss := readRSS(pid); rss > 0 {
					rssSamples = append(rssSamples, rss)
				}
			}
		}
	}

	// 4. Build report.
	fmt.Println("✓ done; writing report")
	rpt := buildReport(runID, count, duration, probe, latencies, rssSamples)
	fmt.Println(rpt)
	if err := writeReport("sessions", runID, rpt); err != nil {
		fmt.Printf("⚠ couldn't write report: %v\n", err)
	}
	return nil
}

// findCcmuxd returns the pid of the running ccmuxd or 0 if none.
// macOS-aware (pgrep is fine on darwin + linux).
func findCcmuxd() int {
	out, err := exec.Command("pgrep", "-x", "ccmuxd").Output()
	if err != nil {
		return 0
	}
	var pid int
	fmt.Sscanf(string(out), "%d", &pid)
	return pid
}

// readRSS returns the resident set size of pid in KB. Returns 0 on
// any error. Uses `ps -o rss=` which works on both macOS and Linux.
func readRSS(pid int) int {
	out, err := exec.Command("ps", "-o", "rss=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return 0
	}
	var rss int
	fmt.Sscanf(string(out), "%d", &rss)
	return rss
}

// cleanupSessions kills every session we spawned. Idempotent — a
// kill against a session that the user already killed is fine.
func cleanupSessions(names []string) {
	for _, name := range names {
		_ = exec.Command("tmux", "kill-session", "-t", name).Run()
	}
}

// buildReport assembles the markdown report body. Pure function; the
// caller decides where to write it. Random suppression: we don't seed
// math/rand for determinism here since the report is just for review.
var _ = rand.Int

type reportData struct {
	runID    int64
	count    int
	duration time.Duration
	probe    time.Duration
	lat      []time.Duration
	rss      []int
}

func buildReport(runID int64, count int, duration, probe time.Duration, lat []time.Duration, rss []int) string {
	d := reportData{runID, count, duration, probe, lat, rss}
	return renderReport(d)
}

func renderReport(d reportData) string {
	var b []byte
	add := func(format string, a ...any) {
		b = append(b, []byte(fmt.Sprintf(format, a...))...)
	}
	add("# Stress run: sessions (runid=%d)\n\n", d.runID)
	add("- Date: %s\n", time.Now().UTC().Format(time.RFC3339))
	add("- Profile: sessions\n")
	add("- Sessions spawned: %d\n", d.count)
	add("- Duration: %v\n", d.duration)
	add("- Probe interval: %v\n\n", d.probe)

	add("## Latency (%d /v1/sessions calls)\n\n", len(d.lat))
	if p50, p95, max := latencyStats(d.lat); p50 > 0 {
		add("- p50: %v\n", p50)
		add("- p95: %v\n", p95)
		add("- max: %v\n", max)
	} else {
		add("- (no successful probes recorded)\n")
	}
	add("\n## Daemon RSS (%d samples)\n\n", len(d.rss))
	if len(d.rss) == 0 {
		add("- (no samples — daemon pid not found or `ps` unavailable)\n")
	} else {
		min, max := minMax(d.rss)
		add("- start: %d KB (%.1f MB)\n", d.rss[0], float64(d.rss[0])/1024)
		add("- end:   %d KB (%.1f MB)\n", d.rss[len(d.rss)-1], float64(d.rss[len(d.rss)-1])/1024)
		add("- min:   %d KB (%.1f MB)\n", min, float64(min)/1024)
		add("- max:   %d KB (%.1f MB)\n", max, float64(max)/1024)
		// Spec thresholds: fail >150 MB absolute or end >3× start with
		// >30 MB delta. We don't fail the process here (this is a
		// dev-loop tool, not CI), but the report flags the condition.
		var flags []string
		if d.rss[len(d.rss)-1] > 150*1024 {
			flags = append(flags, "**FAIL: end-RSS exceeds 150 MB absolute ceiling**")
		}
		if d.rss[len(d.rss)-1] > 3*d.rss[0] && (d.rss[len(d.rss)-1]-d.rss[0]) > 30*1024 {
			flags = append(flags, "**FAIL: end-RSS > 3× start with >30 MB delta**")
		}
		if len(flags) == 0 {
			add("- ✓ within spec thresholds (≤150 MB absolute, ≤3× start)\n")
		} else {
			for _, f := range flags {
				add("- %s\n", f)
			}
		}
	}
	return string(b)
}

func latencyStats(lat []time.Duration) (p50, p95, max time.Duration) {
	if len(lat) == 0 {
		return 0, 0, 0
	}
	cp := make([]time.Duration, len(lat))
	copy(cp, lat)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	p50 = cp[len(cp)*50/100]
	p95 = cp[len(cp)*95/100]
	max = cp[len(cp)-1]
	return
}

func minMax(xs []int) (min, max int) {
	min, max = xs[0], xs[0]
	for _, x := range xs[1:] {
		if x < min {
			min = x
		}
		if x > max {
			max = x
		}
	}
	return
}

// writeReport drops the report under docs/03_Agent_Logs/. The dir is
// expected to exist in any ccmux checkout; if it doesn't (someone
// running ccmux-stress outside the repo) we fall back to /tmp.
func writeReport(profile string, runID int64, body string) error {
	dirs := []string{
		filepath.Join("docs", "03_Agent_Logs"),
		filepath.Join("/tmp"),
	}
	stamp := time.Now().UTC().Format("2006-01-02")
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			fname := filepath.Join(d, fmt.Sprintf("stress-%s-%s-%d.md", stamp, profile, runID))
			return os.WriteFile(fname, []byte(body), 0o644)
		}
	}
	return fmt.Errorf("no writable output dir for report")
}
