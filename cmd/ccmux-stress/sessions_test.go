package main

import (
	"strings"
	"testing"
	"time"
)

// TestLatencyStats covers the p50/p95/max percentiles. Index math is
// off-by-one-prone; an inverted sort or the wrong index would make
// every stress report lie about real performance.
func TestLatencyStats(t *testing.T) {
	cases := []struct {
		name    string
		in      []time.Duration
		wantP50 time.Duration
		wantP95 time.Duration
		wantMax time.Duration
	}{
		{
			"empty returns zeros",
			[]time.Duration{},
			0, 0, 0,
		},
		{
			"single sample",
			[]time.Duration{5 * time.Millisecond},
			5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond,
		},
		{
			// 10 elements; the formula is len*N/100 (integer math, no
			// interpolation). For 10 elements:
			//   p50 = cp[10*50/100] = cp[5] = 6 ms
			//   p95 = cp[10*95/100] = cp[9] = 10 ms
			// These match the actual nearest-rank percentile convention
			// our report uses.
			"sorted ascending — direct indexing",
			[]time.Duration{
				1 * time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond,
				4 * time.Millisecond, 5 * time.Millisecond, 6 * time.Millisecond,
				7 * time.Millisecond, 8 * time.Millisecond, 9 * time.Millisecond,
				10 * time.Millisecond,
			},
			6 * time.Millisecond,
			10 * time.Millisecond,
			10 * time.Millisecond,
		},
		{
			// 5 elements, unsorted; sorts to {1,2,5,7,10}.
			//   p50 = cp[5*50/100] = cp[2] = 5 ms
			//   p95 = cp[5*95/100] = cp[4] = 10 ms
			"unsorted input gets sorted before percentile",
			[]time.Duration{
				10 * time.Millisecond, 1 * time.Millisecond, 5 * time.Millisecond,
				2 * time.Millisecond, 7 * time.Millisecond,
			},
			5 * time.Millisecond,
			10 * time.Millisecond,
			10 * time.Millisecond,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotP50, gotP95, gotMax := latencyStats(tc.in)
			if gotP50 != tc.wantP50 {
				t.Errorf("p50 = %v, want %v", gotP50, tc.wantP50)
			}
			if gotP95 != tc.wantP95 {
				t.Errorf("p95 = %v, want %v", gotP95, tc.wantP95)
			}
			if gotMax != tc.wantMax {
				t.Errorf("max = %v, want %v", gotMax, tc.wantMax)
			}
		})
	}
}

// TestMinMax — small helper but it's used in every stress report's
// RSS block; if min/max swap one row of the report shows wildly wrong
// numbers and people start chasing ghost leaks.
func TestMinMax(t *testing.T) {
	cases := []struct {
		name             string
		in               []int
		wantMin, wantMax int
	}{
		{"singleton", []int{42}, 42, 42},
		{"ascending", []int{1, 2, 3, 4, 5}, 1, 5},
		{"descending", []int{5, 4, 3, 2, 1}, 1, 5},
		{"with duplicates", []int{3, 7, 3, 7, 3}, 3, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotMin, gotMax := minMax(tc.in)
			if gotMin != tc.wantMin {
				t.Errorf("min = %d, want %d", gotMin, tc.wantMin)
			}
			if gotMax != tc.wantMax {
				t.Errorf("max = %d, want %d", gotMax, tc.wantMax)
			}
		})
	}
}

// TestRenderReport_FailFlags is the safety net for the spec's
// leak-detection thresholds. The acceptance criteria in
// docs/01_Specs/03_Testing_And_CI.md call out:
//
//   - Fail if end-RSS > 150 MB absolute
//   - Fail if end-RSS > 3× start-RSS *and* delta > 30 MB
//
// Both rules need to fire in the report so a human reader can act
// on them. If a future refactor drops a rule by mistake, this test
// catches it before the leak it would have flagged ships.
func TestRenderReport_FailFlags(t *testing.T) {
	cases := []struct {
		name       string
		rss        []int
		expectFail string // substring expected in the output, or "" for the OK path
	}{
		{
			"healthy baseline (stable RSS)",
			[]int{20480, 20480, 20480}, // 20 MB constant
			"within spec thresholds",
		},
		{
			"end-RSS exceeds 150 MB absolute",
			[]int{20480, 80000, 200000}, // ends at ~195 MB
			"FAIL: end-RSS exceeds 150 MB absolute",
		},
		{
			"end-RSS > 3× start with > 30 MB delta",
			[]int{10240, 20000, 80000}, // start 10 MB, end ~78 MB (>3× + >30 MB)
			"FAIL: end-RSS > 3× start with >30 MB delta",
		},
		{
			"end-RSS > 3× start but delta below 30 MB threshold",
			[]int{2048, 5000, 8192}, // 2 MB → 8 MB; ratio is high but delta is only 6 MB
			"within spec thresholds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := reportData{
				runID:    1,
				count:    10,
				duration: 30 * time.Second,
				probe:    500 * time.Millisecond,
				rss:      tc.rss,
			}
			got := renderReport(d)
			if !strings.Contains(got, tc.expectFail) {
				t.Errorf("report missing %q:\n%s", tc.expectFail, got)
			}
		})
	}
}

// TestRenderReport_NoSamplesShape — when pgrep doesn't find ccmuxd
// (e.g. daemon not running), the report should still render with a
// clear "no samples" note instead of crashing on len(rss)-1.
func TestRenderReport_NoSamplesShape(t *testing.T) {
	d := reportData{
		runID:    99,
		count:    5,
		duration: 10 * time.Second,
		probe:    500 * time.Millisecond,
		lat:      []time.Duration{5 * time.Millisecond},
	}
	got := renderReport(d)
	if !strings.Contains(got, "no samples") {
		t.Errorf("missing no-samples note:\n%s", got)
	}
}
