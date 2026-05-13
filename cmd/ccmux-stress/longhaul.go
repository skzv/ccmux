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

// newLonghaulCmd builds the `ccmux-stress longhaul` subcommand — the
// 24h-style soak test from the spec.
//
// What this profile tests:
//
//   - Memory leak across thousands of poll iterations. Per the spec,
//     fail at >150 MB absolute end-RSS OR >3× start-RSS with >30 MB
//     delta. Both rules ride alongside as exit codes so the cron-
//     driven runner can wire this into a failing-job alert.
//   - FD leak. Each tmux capture-pane spawns a child process whose
//     pipes must be closed; an unclosed pipe over 24h × 30 polls/min
//     × 4 sessions = 172,800 capture-panes would surface here.
//   - Sleep-manager engage/release lifecycle. With sessions
//     transitioning idle ↔ active over hours, the caffeinate /
//     systemd-inhibit holder gets started + reaped many times. A
//     zombie not reaped would show up as growing process table.
//
// Light steady-state load: 4 sessions (the typical "I have a few
// projects in flight" count), polled every 30s. Sampling cadence is
// 1 sample/minute for RSS; lighter than `sessions` because the run
// length is much longer.
//
// Exit codes:
//
//	0 — clean run, end-state inside spec thresholds
//	1 — threshold violated; report is still written
//	2 — operational failure (daemon unreachable, tmux missing, …)
func newLonghaulCmd() *cobra.Command {
	var (
		duration time.Duration
		sessions int
		sample   time.Duration
	)
	c := &cobra.Command{
		Use:   "longhaul",
		Short: "Multi-hour soak with hard exit codes on the spec's leak thresholds",
		Long: `Run a light, steady-state load against ccmuxd for --duration.
Default 24h with 4 sessions and a 1-minute sampling cadence.

Exits non-zero if the spec's leak thresholds trip:
  - end-RSS > 150 MB absolute
  - end-RSS > 3× start-RSS AND >30 MB delta

The report is appended to as the run progresses so a mid-run crash
still leaves usable evidence under
docs/03_Agent_Logs/stress-<date>-longhaul-<runid>.md.

Intended deployment: a launchd job on the always-on Mac mini per
docs/01_Specs/03_Testing_And_CI.md — GHA-hosted runners cap at
6h/job so a true 24h run can't live there.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			code, err := runLonghaul(cmd.Context(), duration, sessions, sample)
			if err != nil {
				fmt.Fprintln(os.Stderr, "longhaul:", err)
			}
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
	c.Flags().DurationVar(&duration, "duration", 24*time.Hour, "total run length")
	c.Flags().IntVar(&sessions, "sessions", 4, "steady-state session count")
	c.Flags().DurationVar(&sample, "sample-interval", time.Minute, "how often to sample RSS")
	return c
}

// runLonghaul returns (exitCode, error). Splitting them lets the
// caller decide whether a `--dry-run` should propagate non-zero.
// Today the caller always exits with code on threshold violations.
func runLonghaul(ctx context.Context, duration time.Duration, count int, sample time.Duration) (int, error) {
	if duration <= 0 {
		return 2, fmt.Errorf("--duration must be positive")
	}
	if count <= 0 {
		return 2, fmt.Errorf("--sessions must be positive")
	}
	if sample <= 0 || sample > duration {
		return 2, fmt.Errorf("--sample-interval must be in (0, duration]")
	}

	runID := time.Now().UnixMilli()
	prefix := fmt.Sprintf("c-stress-longhaul-%d", runID)
	fmt.Printf("ccmux-stress longhaul: runid=%d sessions=%d duration=%v sample=%v prefix=%s\n",
		runID, count, duration, sample, prefix)

	staged, err := os.MkdirTemp("/tmp", "ccmux-stress-longhaul-")
	if err != nil {
		return 2, err
	}
	defer os.RemoveAll(staged)

	spawned := []string{}
	defer cleanupSessions(spawned)

	for i := 0; i < count; i++ {
		name := fmt.Sprintf("%s-%d", prefix, i)
		work := filepath.Join(staged, fmt.Sprintf("p%d", i))
		if err := os.MkdirAll(work, 0o755); err != nil {
			return 2, err
		}
		// Shell, not `sleep`, so send-keys can deliver. See the
		// matching comment in sessions.go for the history.
		cmd := exec.CommandContext(ctx,
			"tmux", "new-session", "-d",
			"-s", name,
			"-c", work,
			"sh",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			return 2, fmt.Errorf("spawn %s: %w (%s)", name, err, out)
		}
		spawned = append(spawned, name)
	}
	fmt.Printf("✓ spawned %d steady-state sessions\n", len(spawned))

	cli, err := daemon.LocalClient()
	if err != nil {
		return 2, fmt.Errorf("connect to daemon: %w", err)
	}
	pid := findCcmuxd()
	if pid == 0 {
		return 2, fmt.Errorf("can't locate ccmuxd via pgrep")
	}
	fmt.Printf("→ daemon pid=%d, starting %v soak…\n", pid, duration)

	startRSS := readRSS(pid)
	if startRSS == 0 {
		return 2, fmt.Errorf("can't read daemon RSS at start")
	}
	fmt.Printf("→ start RSS: %d KB (%.1f MB)\n", startRSS, float64(startRSS)/1024)

	// Open the report file for append-as-we-go so a mid-run crash
	// still leaves evidence. We write a header and then one line per
	// sample.
	reportPath, err := openLonghaulReport(runID, startRSS, count, duration, sample)
	if err != nil {
		fmt.Printf("⚠ couldn't open report: %v (continuing in-memory)\n", err)
	}

	// Probe ticker pokes the daemon at a 30s cadence so /v1/sessions
	// stays exercised across the run; sample ticker reads RSS at the
	// configured (slower) cadence.
	probeTicker := time.NewTicker(30 * time.Second)
	sampleTicker := time.NewTicker(sample)
	defer probeTicker.Stop()
	defer sampleTicker.Stop()
	deadline := time.Now().Add(duration)

	rssSamples := []int{startRSS}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return 2, ctx.Err()
		case <-probeTicker.C:
			pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
			_, _ = cli.Sessions(pctx)
			pcancel()
		case <-sampleTicker.C:
			rss := readRSS(pid)
			if rss == 0 {
				return 2, fmt.Errorf("daemon disappeared mid-run (pid %d)", pid)
			}
			rssSamples = append(rssSamples, rss)
			appendLonghaulSample(reportPath, time.Now(), rss)
		}
	}

	endRSS := rssSamples[len(rssSamples)-1]
	fail := checkLonghaulThresholds(startRSS, endRSS)
	footer := finalizeLonghaulReport(reportPath, startRSS, endRSS, rssSamples, fail)
	fmt.Println(footer)
	if fail != "" {
		return 1, nil
	}
	return 0, nil
}

// checkLonghaulThresholds returns the empty string when the run is
// inside spec thresholds, or a one-line description of the first
// violated rule. Mirrors the spec exactly so the test catches any
// drift.
func checkLonghaulThresholds(startRSS, endRSS int) string {
	const (
		absoluteCeilingKB = 150 * 1024 // 150 MB
		deltaCeilingKB    = 30 * 1024  // 30 MB
		ratioCeiling      = 3
	)
	if endRSS > absoluteCeilingKB {
		return fmt.Sprintf("end-RSS %d KB (%.1f MB) exceeds 150 MB absolute ceiling", endRSS, float64(endRSS)/1024)
	}
	if endRSS > ratioCeiling*startRSS && (endRSS-startRSS) > deltaCeilingKB {
		return fmt.Sprintf("end-RSS %d KB > 3× start-RSS %d KB with delta %d KB (>30 MB)",
			endRSS, startRSS, endRSS-startRSS)
	}
	return ""
}

// openLonghaulReport writes the report header and returns the file
// path. Returns "" + error when the docs dir isn't available; the
// caller logs and continues without a report file.
func openLonghaulReport(runID int64, startRSS, count int, duration, sample time.Duration) (string, error) {
	dir := filepath.Join("docs", "03_Agent_Logs")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = "/tmp"
	}
	stamp := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(dir, fmt.Sprintf("stress-%s-longhaul-%d.md", stamp, runID))
	header := fmt.Sprintf(`# Stress run: longhaul (runid=%d)

- Date: %s
- Profile: longhaul
- Sessions: %d
- Duration: %v
- Sample interval: %v
- Start RSS: %d KB (%.1f MB)

## Samples (timestamp · RSS KB · RSS MB)

`,
		runID, time.Now().UTC().Format(time.RFC3339),
		count, duration, sample,
		startRSS, float64(startRSS)/1024,
	)
	return path, os.WriteFile(path, []byte(header), 0o644)
}

// appendLonghaulSample appends a single sample line to the report
// file. Silently no-ops if path is "" so callers don't have to
// branch.
func appendLonghaulSample(path string, at time.Time, rss int) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "- %s  %6d KB  %5.1f MB\n",
		at.UTC().Format(time.RFC3339), rss, float64(rss)/1024)
}

// finalizeLonghaulReport appends a summary footer (pass/fail verdict +
// per-spec thresholds) to the report file and returns the same text
// as a string the caller can print. Idempotent re-runs would just
// keep appending footers, but the runner only calls this once.
func finalizeLonghaulReport(path string, startRSS, endRSS int, samples []int, fail string) string {
	min, max := minMax(samples)
	var b []byte
	add := func(format string, a ...any) {
		b = append(b, []byte(fmt.Sprintf(format, a...))...)
	}
	add("\n## Summary\n\n")
	add("- Samples: %d\n", len(samples))
	add("- start RSS: %d KB (%.1f MB)\n", startRSS, float64(startRSS)/1024)
	add("- end   RSS: %d KB (%.1f MB)\n", endRSS, float64(endRSS)/1024)
	add("- min   RSS: %d KB (%.1f MB)\n", min, float64(min)/1024)
	add("- max   RSS: %d KB (%.1f MB)\n", max, float64(max)/1024)
	if fail == "" {
		add("- ✓ inside spec thresholds (≤150 MB absolute, ≤3× start)\n")
	} else {
		add("- **FAIL: %s**\n", fail)
	}
	body := string(b)
	if path != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString(body)
			_ = f.Close()
		}
	}
	return body
}
