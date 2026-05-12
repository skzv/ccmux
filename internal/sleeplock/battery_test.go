package sleeplock

import (
	"strings"
	"testing"
)

// realPmsetSamples are the actual outputs `pmset -g batt` produced on
// a current macOS laptop in different power states. Pinning the parser
// against the live shape means a future macOS version that changes the
// format will trip the test before it silently breaks the daemon.
var realPmsetSamples = []struct {
	name string
	in   string
	want BatteryStatus
}{
	{
		name: "discharging at 87%",
		in: `Now drawing from 'Battery Power'
 -InternalBattery-0 (id=12345678)	87%; discharging; 4:12 remaining present: true`,
		want: BatteryStatus{Percent: 87, OnAC: false, HasBattery: true},
	},
	{
		name: "charging at 42%",
		in: `Now drawing from 'AC Power'
 -InternalBattery-0 (id=12345678)	42%; charging; 0:45 remaining present: true`,
		want: BatteryStatus{Percent: 42, OnAC: true, HasBattery: true},
	},
	{
		name: "single digit (10%) doesn't off-by-one",
		in: `Now drawing from 'Battery Power'
 -InternalBattery-0 (id=12345678)	5%; discharging; 0:15 remaining present: true`,
		want: BatteryStatus{Percent: 5, OnAC: false, HasBattery: true},
	},
	{
		name: "full battery (100%) parses without overflow",
		in: `Now drawing from 'AC Power'
 -InternalBattery-0 (id=12345678)	100%; charged; 0:00 remaining present: true`,
		want: BatteryStatus{Percent: 100, OnAC: true, HasBattery: true},
	},
	{
		name: "desktop / Mac mini (no battery line)",
		in:   `Now drawing from 'AC Power'`,
		want: BatteryStatus{Percent: 0, OnAC: true, HasBattery: false},
	},
}

func TestParsePmsetBatt(t *testing.T) {
	for _, tc := range realPmsetSamples {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePmsetBatt(tc.in)
			if got != tc.want {
				t.Errorf("parsePmsetBatt =\n  got  %+v\n  want %+v\n  input:\n%s",
					got, tc.want, indent(tc.in))
			}
		})
	}
}

// TestParsePmsetBatt_RobustToWhitespace covers the trailing/leading
// whitespace + Windows-CRLF stuff that sometimes shows up when callers
// fan output through other tools.
func TestParsePmsetBatt_RobustToWhitespace(t *testing.T) {
	in := "  Now drawing from 'Battery Power'  \r\n" +
		"\t-InternalBattery-0 (id=1)\t77%; discharging; 3:00 remaining present: true\r\n"
	got := parsePmsetBatt(in)
	want := BatteryStatus{Percent: 77, OnAC: false, HasBattery: true}
	if got != want {
		t.Errorf("got %+v want %+v", got, want)
	}
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}
