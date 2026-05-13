package main

import (
	"strings"
	"testing"
)

// TestCheckLonghaulThresholds is the safety net for the spec's
// leak-detection rules, mirroring the same coverage that
// TestRenderReport_FailFlags has for the sessions subcommand but
// against the exit-code helper that longhaul uses. If either rule
// drifts here, the cron-driven runner stops alerting on real
// regressions.
func TestCheckLonghaulThresholds(t *testing.T) {
	const MB = 1024 // kilobytes per megabyte

	cases := []struct {
		name          string
		startRSS      int
		endRSS        int
		wantViolation string // substring expected, or "" for the OK path
	}{
		{
			"stable run — within thresholds",
			20 * MB, 21 * MB,
			"",
		},
		{
			"end-RSS just over 150 MB ceiling",
			20 * MB, 151 * MB,
			"exceeds 150 MB absolute ceiling",
		},
		{
			"end-RSS 200 MB — well over absolute",
			50 * MB, 200 * MB,
			"exceeds 150 MB absolute ceiling",
		},
		{
			"end > 3× start AND delta > 30 MB",
			10 * MB, 50 * MB, // 5× start, delta 40 MB
			"> 3× start-RSS",
		},
		{
			"end > 3× start but delta only 25 MB",
			5 * MB, 20 * MB, // 4× start, delta 15 MB — under 30 MB
			"",
		},
		{
			"end > 3× start AND delta > 30 MB AND > 150 MB",
			30 * MB, 200 * MB,
			"exceeds 150 MB absolute ceiling", // absolute check fires first
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkLonghaulThresholds(tc.startRSS, tc.endRSS)
			if tc.wantViolation == "" {
				if got != "" {
					t.Errorf("expected no violation, got: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantViolation) {
				t.Errorf("violation msg %q doesn't mention %q", got, tc.wantViolation)
			}
		})
	}
}

// TestFinalizeLonghaulReport_FailVerdict — when a threshold violates,
// the summary footer must contain "FAIL: …" so a human reviewing the
// markdown file sees what went wrong at a glance. The ✓ verdict is
// reserved for clean runs.
func TestFinalizeLonghaulReport_FailVerdict(t *testing.T) {
	const MB = 1024
	body := finalizeLonghaulReport("", 20*MB, 200*MB,
		[]int{20 * MB, 100 * MB, 200 * MB},
		"end-RSS exceeds 150 MB absolute ceiling",
	)
	if !strings.Contains(body, "**FAIL:") {
		t.Errorf("missing FAIL prefix in body:\n%s", body)
	}
	if strings.Contains(body, "inside spec thresholds") {
		t.Errorf("fail verdict should not also say 'inside spec thresholds':\n%s", body)
	}
}

// TestFinalizeLonghaulReport_OKVerdict pins the happy path's wording
// so a future refactor doesn't drop the ✓ — losing the visual cue
// would slow down humans reading hundreds of nightly reports.
func TestFinalizeLonghaulReport_OKVerdict(t *testing.T) {
	const MB = 1024
	body := finalizeLonghaulReport("", 20*MB, 21*MB, []int{20 * MB, 20*MB + 100, 21 * MB}, "")
	if !strings.Contains(body, "✓ inside spec thresholds") {
		t.Errorf("missing OK verdict:\n%s", body)
	}
}
