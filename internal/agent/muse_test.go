package agent

import (
	"reflect"
	"testing"
	"time"
)

func TestMuseLaunchAndResume(t *testing.T) {
	commands := Commands{Muse: "/tmp/Muse Code/muse"}
	if id, ok := ParseID(" MUSE-CODE "); !ok || id != IDMuse {
		t.Fatal(id, ok)
	}
	if got := LaunchCmd(IDMuse, false, commands); got != "'/tmp/Muse Code/muse'" {
		t.Fatal(got)
	}
	if got := LaunchCmd(IDMuse, true, commands); got != "'/tmp/Muse Code/muse' resume --last || '/tmp/Muse Code/muse' || zsh || bash || sh" {
		t.Fatal(got)
	}
	if got := ResumeArgs(IDMuse, "a-session", commands); !reflect.DeepEqual(got, []string{"/tmp/Muse Code/muse", "resume", "a-session"}) {
		t.Fatal(got)
	}
}

func TestMuseCapturedStates(t *testing.T) {
	a := Muse{}
	for _, tc := range []struct {
		pane string
		want State
	}{
		{"Do you trust this workspace?\n> 1 Trust and continue\nUse Up/Down or 1/2, then Enter. Esc quits.", StateNeedsInput},
		{"◆ Thinking (1m 21s · esc to interrupt)\n  └ attempt 8/10 · retrying in 46s · meta api client\n⟩\n────────\nmeta · /projects/demo", StateActive},
		{"◇ Thinking\n⟩\n────────\nmeta · /projects/demo", StateActive},
		{"◆ Finished\n⟩ Type @ to search and insert workspace file paths\n────────\nmeta · /projects/demo", StateNeedsInput},
		{"Start building with a monthly plan · /upgrade (https://accountscenter.meta.com/muse_code/?ep=no_payg)\n⟩ A pending prompt\n────────\nmuse-spark-1.3-contributor · high · /projects/demo", StateNeedsInput},
		{"◆ model failed: API error 402: Billing verification failed. Please check your payment method.\n⟩\n────────\nmeta · /projects/demo", StateNeedsInput},
		{"", StateUnknown},
	} {
		if got := a.Classify(tc.pane, time.Now().Add(-time.Minute), time.Second); got != tc.want {
			t.Fatalf("%q: %s, want %s", tc.pane, got, tc.want)
		}
	}
	if got := a.Classify("⟩\n────────\nmeta · /projects/demo", time.Now(), time.Second); got != StateActive {
		t.Fatal("prompt redraw must retain idle gate", got)
	}
}
