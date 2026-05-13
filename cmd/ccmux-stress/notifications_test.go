package main

import (
	"strings"
	"testing"
	"time"
)

// TestPercentile mirrors the test for latencyStats but for the
// single-stat helper. The integer math (len*p/100, idx-clamp on
// the upper end) is easy to get wrong by one.
func TestPercentile(t *testing.T) {
	cases := []struct {
		name string
		in   []time.Duration
		p    int
		want time.Duration
	}{
		{"empty → 0", nil, 50, 0},
		{"single sample", []time.Duration{5 * time.Millisecond}, 50, 5 * time.Millisecond},
		{
			"p50 of 10 sorted asc",
			[]time.Duration{
				1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
			},
			50,
			6, // idx 10*50/100 = 5 → cp[5] = 6
		},
		{
			"p95 of 10 sorted asc",
			[]time.Duration{
				1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
			},
			95,
			10, // idx 10*95/100 = 9 → cp[9] = 10
		},
		{
			"p100 clamps to last index",
			[]time.Duration{1, 2, 3},
			100,
			3, // raw idx would be 3 (out of range); clamped to len-1
		},
		{
			"unsorted input sorts internally",
			[]time.Duration{9, 1, 4, 7, 2},
			50,
			4, // sorted {1,2,4,7,9}, idx 5*50/100 = 2 → 4
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentile(tc.in, tc.p); got != tc.want {
				t.Errorf("percentile(%v, %d) = %v, want %v", tc.in, tc.p, got, tc.want)
			}
		})
	}
}

// TestBuildNotifReport_NoDetection — when the daemon doesn't detect
// any transitions (broken classifier? wedged poll loop?), the report
// must say so explicitly. A silent "no data" report would let the
// next person assume the daemon is healthy.
func TestBuildNotifReport_NoDetection(t *testing.T) {
	got := buildNotifReport(1, 50, 5*time.Second, 30*time.Second, map[string]time.Duration{}, 50)
	if !strings.Contains(got, "0 / 50") {
		t.Errorf("missing detection-count fraction:\n%s", got)
	}
	if !strings.Contains(got, "no transitions detected") {
		t.Errorf("missing zero-detection diagnostic:\n%s", got)
	}
}

// TestBuildNotifReport_WithinEnvelope — happy path. All transitions
// detected fast; report confirms the ≤10s p95 envelope.
func TestBuildNotifReport_WithinEnvelope(t *testing.T) {
	seen := map[string]time.Duration{}
	// 10 sessions, each detected at ~5s (the expected baseline:
	// 2s poll interval + 3s idle threshold).
	for i := 0; i < 10; i++ {
		seen[stringForKey(i)] = 5500 * time.Millisecond
	}
	got := buildNotifReport(1, 10, 2*time.Second, 30*time.Second, seen, 10)
	if !strings.Contains(got, "10 / 10") {
		t.Errorf("missing all-detected fraction:\n%s", got)
	}
	if !strings.Contains(got, "within expected envelope") {
		t.Errorf("missing within-envelope verdict:\n%s", got)
	}
	if strings.Contains(got, "WARN") {
		t.Errorf("should not warn on a 5.5s p95:\n%s", got)
	}
}

// TestBuildNotifReport_StarvationWarning — when detection takes
// >10s, the report should flag the daemon as starving. This is the
// regression-protection arm: a future change that quietly raises
// the daemon's poll interval would surface as a warning here
// instead of as a silent latency regression in production.
func TestBuildNotifReport_StarvationWarning(t *testing.T) {
	seen := map[string]time.Duration{
		"a": 12 * time.Second,
		"b": 15 * time.Second,
		"c": 20 * time.Second,
		"d": 25 * time.Second,
		"e": 30 * time.Second,
	}
	got := buildNotifReport(1, 5, 2*time.Second, 30*time.Second, seen, 5)
	if !strings.Contains(got, "WARN") {
		t.Errorf("missing starvation warning for slow p95:\n%s", got)
	}
}

// stringForKey just gives each session a distinct name for the test;
// the report's percentile math doesn't care about the keys.
func stringForKey(i int) string {
	return string(rune('a' + i))
}
