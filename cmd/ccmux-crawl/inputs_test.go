package main

import (
	"math/rand"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestKeyChoices_AllReachable — the curated keyChoices list is what
// the crawler can ever emit. If anyone ever drops an entry, crawl
// coverage shrinks silently. Test pins:
//
//   - All seven screen-switch numbers (1..7) are present
//   - Every navigation key (up/down/left/right, tab, enter, esc) is
//     represented at least once
//
// If a future feature binds a new keymap that the crawler doesn't
// know about, doing the same thing for that key is a one-line PR
// against this test.
func TestKeyChoices_AllReachable(t *testing.T) {
	must := map[string]bool{
		"rune('1')": false, "rune('2')": false, "rune('3')": false,
		"rune('4')": false, "rune('5')": false, "rune('6')": false,
		"rune('7')": false,
		"up":        false, "down": false, "left": false, "right": false,
		"tab": false, "enter": false, "esc": false,
	}
	for _, c := range keyChoices {
		if _, ok := must[c.Label]; ok {
			must[c.Label] = true
		}
	}
	for label, seen := range must {
		if !seen {
			t.Errorf("keyChoices missing required entry: %s", label)
		}
	}
}

// TestRandomKey_ReturnsOnlyFromCorpus — the crawler must never invent
// a key outside the curated list (that would be the bubbletea byte-
// parser's job, which we deliberately don't fuzz here per the spec).
// Property test driven by a small fixed seed; verifies the corpus
// invariant rather than the distribution.
func TestRandomKey_ReturnsOnlyFromCorpus(t *testing.T) {
	corpusLabels := map[string]bool{}
	for _, c := range keyChoices {
		corpusLabels[c.Label] = true
	}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000; i++ {
		got := randomKey(rng)
		if !corpusLabels[got.Label] {
			t.Fatalf("randomKey returned %q (msg=%T) which isn't in keyChoices", got.Label, got.Msg)
		}
		if _, ok := got.Msg.(tea.KeyMsg); !ok {
			t.Fatalf("randomKey returned non-KeyMsg: %T", got.Msg)
		}
	}
}

// TestRandomResize_DimensionsBounded — the crawler's resize generator
// has both a floor (1) and a ceiling (300×100) on the dimensions so
// the test machine doesn't burn cycles on absurd sizes. Property
// test verifies the bounds hold across many draws.
func TestRandomResize_DimensionsBounded(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000; i++ {
		got := randomResize(rng)
		sz, ok := got.Msg.(tea.WindowSizeMsg)
		if !ok {
			t.Fatalf("randomResize returned non-WindowSizeMsg: %T", got.Msg)
		}
		if sz.Width < 1 || sz.Width > 300 {
			t.Errorf("Width=%d outside [1,300]", sz.Width)
		}
		if sz.Height < 1 || sz.Height > 100 {
			t.Errorf("Height=%d outside [1,100]", sz.Height)
		}
		if !strings.HasPrefix(got.Label, "resize(") {
			t.Errorf("label %q doesn't read like a resize", got.Label)
		}
	}
}

// TestFormatSequence_HasIndexAndLabel — crash reports need both the
// iteration number (so the user can grep for the panic point) and
// the label (so they can read what the input was) on every line.
// Test pins the format with a few rows.
func TestFormatSequence_HasIndexAndLabel(t *testing.T) {
	seq := []Input{
		keyRune('a'),
		keyType(tea.KeyEnter, "enter"),
		{Msg: tea.WindowSizeMsg{Width: 80, Height: 24}, Label: "resize(80x24)"},
	}
	got := formatSequence(seq)
	for i, want := range []string{
		"    0  rune('a')",
		"    1  enter",
		"    2  resize(80x24)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatSequence missing row %d %q:\n%s", i, want, got)
		}
	}
}

// TestNewRNG_ZeroSeedPicksClock — a seed of 0 means "pick from the
// clock so the run is non-deterministic"; the seed *used* gets
// stored so the crash report can echo it. If newRNG returned 0 in
// that case, the user couldn't reproduce the failure.
func TestNewRNG_ZeroSeedPicksClock(t *testing.T) {
	got := newRNG(0)
	if got.seed == 0 {
		t.Errorf("newRNG(0) left seed at 0 — should have substituted a clock-derived value")
	}
}

// TestNewRNG_ExplicitSeedRoundTrips — when the user passes --seed=N,
// the rngSource must preserve N for the crash-report echo.
func TestNewRNG_ExplicitSeedRoundTrips(t *testing.T) {
	got := newRNG(12345)
	if got.seed != 12345 {
		t.Errorf("newRNG(12345).seed = %d, want 12345", got.seed)
	}
}
