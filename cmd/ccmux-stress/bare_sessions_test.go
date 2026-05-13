package main

import (
	"strings"
	"testing"
	"time"
)

// TestRenderBareReport_OKPath — zero errors and fast p95 must produce
// the "within spec thresholds" verdict, not a FAIL line.
func TestRenderBareReport_OKPath(t *testing.T) {
	d := bareReportData{
		runID:       1,
		count:       20,
		concurrency: 5,
		lats:        []time.Duration{100 * time.Millisecond, 200 * time.Millisecond},
		errs:        nil,
		created:     20,
	}
	got := renderBareReport(d)
	if strings.Contains(got, "FAIL:") {
		t.Errorf("healthy run produced FAIL verdict:\n%s", got)
	}
	if !strings.Contains(got, "within spec thresholds") {
		t.Errorf("healthy run missing OK verdict:\n%s", got)
	}
}

// TestRenderBareReport_ErrorRateThreshold — >5% errors must trigger FAIL.
func TestRenderBareReport_ErrorRateThreshold(t *testing.T) {
	// 2 errors out of 10 = 20% error rate.
	lats := make([]time.Duration, 10)
	for i := range lats {
		lats[i] = 50 * time.Millisecond
	}
	d := bareReportData{
		runID:       2,
		count:       10,
		concurrency: 5,
		lats:        lats,
		errs:        []string{"connection refused", "connection refused"},
		created:     8,
	}
	got := renderBareReport(d)
	if !strings.Contains(got, "FAIL: error rate") {
		t.Errorf("expected error-rate FAIL, got:\n%s", got)
	}
}

// TestRenderBareReport_P95LatencyThreshold — p95 > 5s must trigger FAIL.
func TestRenderBareReport_P95LatencyThreshold(t *testing.T) {
	// Build 20 latency samples where the top 5% exceeds 5s.
	lats := make([]time.Duration, 20)
	for i := range lats {
		lats[i] = 100 * time.Millisecond
	}
	// The last element will be at p95 = lats[20*95/100] = lats[19].
	lats[19] = 6 * time.Second
	d := bareReportData{
		runID:       3,
		count:       20,
		concurrency: 5,
		lats:        lats,
		errs:        nil,
		created:     20,
	}
	got := renderBareReport(d)
	if !strings.Contains(got, "FAIL: p95 latency") {
		t.Errorf("expected p95 FAIL, got:\n%s", got)
	}
}

// TestRenderBareReport_ErrorDeduplication — repeated errors should be
// collapsed into a single "×N" line so systemic failures stay readable.
func TestRenderBareReport_ErrorDeduplication(t *testing.T) {
	d := bareReportData{
		runID:       4,
		count:       10,
		concurrency: 5,
		lats:        []time.Duration{100 * time.Millisecond},
		errs:        []string{"timeout", "timeout", "timeout"},
		created:     7,
	}
	got := renderBareReport(d)
	if !strings.Contains(got, "×3") {
		t.Errorf("expected deduplicated error ×3, got:\n%s", got)
	}
	// The raw error message should appear only once despite 3 occurrences.
	count := strings.Count(got, "timeout")
	if count != 1 {
		t.Errorf("error message appeared %d times, want 1 (deduplicated)", count)
	}
}
