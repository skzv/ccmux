package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/daemon"
)

// newBareSessionsCmd builds the `ccmux-stress bare-sessions` subcommand.
// It hammers the local ccmuxd with concurrent POST /v1/sessions/bare
// requests to surface races in the session-creation handler, detect FD
// leaks from concurrent clients, and measure latency under concurrent load.
//
// Sessions are named `c-stress-bare-<runid>-N` so a failed cleanup can be
// swept with `tmux kill-session -t c-stress-bare-*`.
func newBareSessionsCmd() *cobra.Command {
	var (
		count       int
		concurrency int
		keepAlive   time.Duration
	)
	c := &cobra.Command{
		Use:   "bare-sessions",
		Short: "Hammer ccmuxd with N concurrent bare-session creation requests",
		Long: `Send --count POST /v1/sessions/bare requests to the local ccmuxd
with up to --concurrency in flight at once. After all requests complete
(success or error), wait --keep-alive, then kill every session created.

Reports: per-request latency p50/p95/max, success vs error counts, list
of error messages if any, and pass/fail verdict (fail if >5% error rate
or p95 > 5s).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBareSessions(cmd.Context(), count, concurrency, keepAlive)
		},
	}
	c.Flags().IntVar(&count, "count", 20, "total bare sessions to create")
	c.Flags().IntVar(&concurrency, "concurrency", 5, "max concurrent requests in flight")
	c.Flags().DurationVar(&keepAlive, "keep-alive", 3*time.Second, "how long to leave sessions alive before cleanup")
	return c
}

type bareSessionResult struct {
	name    string
	latency time.Duration
	err     error
}

func runBareSessions(ctx context.Context, count, concurrency int, keepAlive time.Duration) error {
	if count <= 0 {
		return fmt.Errorf("--count must be positive")
	}
	if concurrency <= 0 {
		return fmt.Errorf("--concurrency must be positive")
	}

	cli, err := daemon.LocalClient()
	if err != nil {
		return fmt.Errorf("connect to local ccmuxd: %w", err)
	}

	runID := time.Now().UnixMilli()
	fmt.Printf("ccmux-stress bare-sessions: runid=%d count=%d concurrency=%d\n", runID, count, concurrency)

	sem := make(chan struct{}, concurrency)
	results := make([]bareSessionResult, count)
	var wg sync.WaitGroup

	for i := 0; i < count; i++ {
		wg.Add(1)
		i := i
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			name := fmt.Sprintf("c-stress-bare-%d-%d", runID, i)
			reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			start := time.Now()
			res, reqErr := cli.NewBareSession(reqCtx, daemon.NewBareSessionRequest{
				Name: name,
				Path: "/tmp",
			})
			lat := time.Since(start)

			r := bareSessionResult{latency: lat, err: reqErr}
			if reqErr == nil {
				r.name = res.Session
			}
			results[i] = r
		}()
	}
	wg.Wait()

	// Collect created session names for cleanup, latencies for stats.
	var lats []time.Duration
	var errs []string
	var created []string
	for _, r := range results {
		lats = append(lats, r.latency)
		if r.err != nil {
			errs = append(errs, r.err.Error())
		} else if r.name != "" {
			created = append(created, r.name)
		}
	}

	if keepAlive > 0 {
		fmt.Printf("  holding %d sessions for %v before cleanup…\n", len(created), keepAlive)
		select {
		case <-time.After(keepAlive):
		case <-ctx.Done():
		}
	}

	// Kill created sessions.
	cleanupSessions(created)

	report := renderBareReport(bareReportData{
		runID:       runID,
		count:       count,
		concurrency: concurrency,
		lats:        lats,
		errs:        errs,
		created:     len(created),
	})
	fmt.Println(report)
	if strings.Contains(report, "FAIL:") {
		return fmt.Errorf("stress run did not meet acceptance criteria")
	}
	return nil
}

type bareReportData struct {
	runID       int64
	count       int
	concurrency int
	lats        []time.Duration
	errs        []string
	created     int
}

func renderBareReport(d bareReportData) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n## bare-sessions stress report (runid %d)\n\n", d.runID))
	sb.WriteString(fmt.Sprintf("- count: %d  concurrency: %d\n", d.count, d.concurrency))
	sb.WriteString(fmt.Sprintf("- created: %d  errors: %d\n", d.created, len(d.errs)))

	p50, p95, maxLat := latencyStats(d.lats)
	sb.WriteString(fmt.Sprintf("- latency  p50=%v  p95=%v  max=%v\n", p50, p95, maxLat))

	if len(d.errs) > 0 {
		sb.WriteString("\n### errors\n")
		// Deduplicate and cap output to avoid wall-of-text for systemic failures.
		seen := map[string]int{}
		for _, e := range d.errs {
			seen[e]++
		}
		sorted := make([]string, 0, len(seen))
		for k := range seen {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, e := range sorted {
			sb.WriteString(fmt.Sprintf("  - (×%d) %s\n", seen[e], e))
		}
	}

	sb.WriteString("\n### verdict\n")
	failed := false
	errorRate := float64(len(d.errs)) / float64(d.count)
	if errorRate > 0.05 {
		sb.WriteString(fmt.Sprintf("  FAIL: error rate %.1f%% exceeds 5%% threshold\n", errorRate*100))
		failed = true
	}
	const p95Limit = 5 * time.Second
	if p95 > p95Limit {
		sb.WriteString(fmt.Sprintf("  FAIL: p95 latency %v exceeds %v threshold\n", p95, p95Limit))
		failed = true
	}
	if !failed {
		sb.WriteString("  OK: within spec thresholds\n")
	}
	return sb.String()
}
