package main

import (
	"fmt"
	"math/rand"

	tea "github.com/charmbracelet/bubbletea"
)

// Input is one element of a crawl sequence — wraps a tea.Msg with
// enough metadata to print a human-readable line in the crash
// report. Two reasons we don't just store tea.Msg:
//
//  1. tea.KeyMsg's Type values are integers; printing them as numbers
//     in a crash report is unhelpful. The Label gives a readable name.
//  2. Sequence summarization needs deterministic ordering; the Label
//     also serves as the stable key for "this sequence triggered the
//     bug".
type Input struct {
	Msg   tea.Msg
	Label string
}

// keyChoices is the curated alphabet of KeyMsgs the crawler can
// send. We don't crawl the full Unicode space — bubbletea's input
// parser is upstream, well-tested, and not what we're trying to
// stress here. The list focuses on:
//
//   - Every screen-switch number (1..7)
//   - Every named key the ccmux keymap binds (q, ?, T, /, etc.)
//   - The navigation primitives (arrows, tab, enter, esc)
//   - A handful of plain ASCII runes the form fields type into
//
// Adding new entries here is the standard way to widen crawl
// coverage when a new feature ships.
var keyChoices = []Input{
	// Screen switches.
	keyRune('1'), keyRune('2'), keyRune('3'), keyRune('4'),
	keyRune('5'), keyRune('6'), keyRune('7'),

	// Per-screen keymap bindings.
	keyRune('n'), keyRune('u'), keyRune('a'), keyRune('x'),
	keyRune('r'), keyRune('R'), keyRune('k'), keyRune('?'),
	keyRune('T'), keyRune('q'), keyRune('/'), keyRune('e'),
	keyRune('y'), keyRune('Y'),

	// Navigation.
	keyType(tea.KeyUp, "up"),
	keyType(tea.KeyDown, "down"),
	keyType(tea.KeyLeft, "left"),
	keyType(tea.KeyRight, "right"),
	keyType(tea.KeyTab, "tab"),
	keyType(tea.KeyShiftTab, "shift+tab"),
	keyType(tea.KeyEnter, "enter"),
	keyType(tea.KeyEsc, "esc"),
	keyType(tea.KeyBackspace, "backspace"),
	keyType(tea.KeySpace, "space"),

	// A handful of typeable characters so form fields can be
	// populated. Not exhaustive — fuzzing single characters in form
	// fields is what the agent.ParseID / ReadAgent fuzzers cover.
	keyRune('h'), keyRune('e'), keyRune('l'), keyRune('o'),
	keyRune('w'), keyRune('s'), keyRune('t'), keyRune('-'),
	keyRune('_'), keyRune('0'), keyRune('z'),
}

// keyRune is a constructor for an Input wrapping a printable rune.
func keyRune(r rune) Input {
	return Input{
		Msg:   tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}},
		Label: fmt.Sprintf("rune(%q)", r),
	}
}

// keyType wraps a non-rune KeyType (arrows, tab, etc.) with a label.
func keyType(t tea.KeyType, label string) Input {
	return Input{Msg: tea.KeyMsg{Type: t}, Label: label}
}

// randomKey returns one of the curated KeyMsg inputs uniformly.
// Caller seeds rng; the deterministic seed is what makes a crash
// report a reproducer.
func randomKey(rng *rand.Rand) Input {
	return keyChoices[rng.Intn(len(keyChoices))]
}

// randomResize returns a tea.WindowSizeMsg with bounded dimensions.
// We allow widths down to 1 cell because the dashboard layout has
// narrow-mode branches at <80 cols; if those branches panic we want
// to find out. Upper bound of 300×100 is well past the realistic
// terminal sizes ccmux sees.
func randomResize(rng *rand.Rand) Input {
	w := 1 + rng.Intn(300)
	h := 1 + rng.Intn(100)
	return Input{
		Msg:   tea.WindowSizeMsg{Width: w, Height: h},
		Label: fmt.Sprintf("resize(%dx%d)", w, h),
	}
}

// resizeInput returns an Input wrapping a tea.WindowSizeMsg with the
// given dimensions. Convenience constructor for scenario steps that
// want to send a specific size (as opposed to randomResize, which
// picks dimensions from an rng).
func resizeInput(w, h int) Input {
	return Input{
		Msg:   tea.WindowSizeMsg{Width: w, Height: h},
		Label: fmt.Sprintf("resize(%dx%d)", w, h),
	}
}

// formatSequence renders an input sequence as one entry per line,
// numbered for grepability against the iter index where a panic
// happened. Used by the crash report writer.
func formatSequence(seq []Input) string {
	var b []byte
	for i, in := range seq {
		b = append(b, []byte(fmt.Sprintf("  %5d  %s\n", i, in.Label))...)
	}
	return string(b)
}
