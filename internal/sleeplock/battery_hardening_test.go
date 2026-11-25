package sleeplock

import "testing"

func TestParsePmsetBatt_MissingPercentDoesNotFakeZeroBattery(t *testing.T) {
	in := "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1)\tno percent reported\n"
	got := parsePmsetBatt(in)
	if got.HasBattery {
		t.Errorf("parsePmsetBatt with unreadable percent returned HasBattery=true (Percent=%d), want HasBattery=false", got.Percent)
	}
}
