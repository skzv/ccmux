package agentdetect

import "testing"

func TestKimiAndKiro_NoFalsePositiveWorking(t *testing.T) {
	kimiRules := RulesFor("kimi")
	if len(kimiRules) == 0 {
		t.Fatal("missing kimi rules")
	}
	res := Evaluate(kimiRules, Input{
		Pane: "cwd: ~/networking-lab\nstatus: not working yet\n> ",
	})
	if res.State == StateActive {
		t.Errorf("kimi falsely matched active on prose/path: %+v", res)
	}

	kiroRules := RulesFor("kiro")
	if len(kiroRules) == 0 {
		t.Fatal("missing kiro rules")
	}
	res = Evaluate(kiroRules, Input{
		Pane: "● Done editing main.go\n> ",
	})
	if res.State == StateActive {
		t.Errorf("kiro falsely matched active on bullet ●: %+v", res)
	}

	claudeRules := RulesFor("claude")
	res = Evaluate(claudeRules, Input{Title: " ⠋ Working"})
	if res.State != StateActive {
		t.Errorf("claude leading-space spinner title = %+v, want active", res)
	}
}
