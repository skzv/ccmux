package sleeplock

import (
	"strings"
	"testing"
)

// FuzzParsePmsetBatt exercises the macOS battery-info parser with
// arbitrary `pmset -g batt` output shapes. Contract:
//
//  1. parsePmsetBatt never panics.
//  2. Percent stays in [0, 100]. An out-of-range value would cascade
//     into the sleep manager's low-battery cutoff comparison and
//     either falsely keep dangerous mode active forever (negative)
//     or never trip the cutoff (>100).
//  3. OnAC and HasBattery are bools — Go's type system handles that;
//     we assert HasBattery requires *some* parsed signal.
//
// This is the most likely source of a future regression: Apple
// occasionally tweaks the `pmset -g batt` output across macOS
// versions, and our string-matching parser has limited tolerance
// for shape drift. Fuzz catches the next change before it ships.
func FuzzParsePmsetBatt(f *testing.F) {
	// Real-world samples from battery_test.go as seeds.
	for _, seed := range []string{
		"Now drawing from 'Battery Power'\n -InternalBattery-0 (id=12345678)\t87%; discharging; 4:12 remaining present: true",
		"Now drawing from 'AC Power'\n -InternalBattery-0 (id=12345678)\t42%; charging; 0:45 remaining present: true",
		"Now drawing from 'AC Power'",
		"",
		"\x00\x00",
		"Now drawing from 'Battery Power'\nasdf% wat\n",
		"100% percent% percent% 999%",                                              // multiple %-bearing strings
		"-InternalBattery-0\t999%; charging",                                       // value out of plausible range
		strings.Repeat("Now drawing from 'AC Power'\n-InternalBattery-0\t50%", 50), // duplicated lines
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := parsePmsetBatt(raw)
		if got.Percent < 0 || got.Percent > 100 {
			t.Fatalf("parsePmsetBatt(%q) returned Percent=%d (out of [0,100])", raw, got.Percent)
		}
		// HasBattery=true must mean we actually parsed an InternalBattery
		// line. If we report HasBattery on a string that mentions
		// nothing battery-flavored, the sleep manager will engage the
		// monitor pointlessly. Substring check keeps this honest.
		if got.HasBattery && !strings.Contains(raw, "InternalBattery") {
			t.Fatalf("parsePmsetBatt(%q) set HasBattery=true with no InternalBattery line in input", raw)
		}
	})
}
