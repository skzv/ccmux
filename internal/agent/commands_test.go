package agent

import "testing"

func hasCommand(cmds []AgentCommand, name string) (AgentCommand, bool) {
	for _, c := range cmds {
		if c.Name == name {
			return c, true
		}
	}
	return AgentCommand{}, false
}

func TestBuiltinCommands_Claude(t *testing.T) {
	cmds := BuiltinCommands(Claude{})
	if len(cmds) == 0 {
		t.Fatal("Claude should advertise built-in commands")
	}
	model, ok := hasCommand(cmds, "/model")
	if !ok {
		t.Fatal("Claude catalog should include /model")
	}
	if !model.TakesArg {
		t.Errorf("/model should take an argument")
	}
	if _, ok := hasCommand(cmds, "/compact"); !ok {
		t.Errorf("Claude catalog should include /compact")
	}
}

func TestBuiltinCommands_CodexDiffersFromClaude(t *testing.T) {
	codex := BuiltinCommands(Codex{})
	if len(codex) == 0 {
		t.Fatal("Codex should advertise built-in commands")
	}
	// /undo is Codex-specific; it must not appear in Claude's catalog,
	// proving the catalog is per-agent, not a shared list.
	if _, ok := hasCommand(codex, "/undo"); !ok {
		t.Errorf("Codex catalog should include /undo")
	}
	if _, ok := hasCommand(BuiltinCommands(Claude{}), "/undo"); ok {
		t.Errorf("/undo must not leak into Claude's catalog")
	}
}

func TestBuiltinCommands_PromptOnlyAgent(t *testing.T) {
	// Cursor doesn't implement CommandfulAgent yet → prompt-only (nil
	// catalog). This guards the optional-interface contract.
	if cmds := BuiltinCommands(Cursor{}); cmds != nil {
		t.Errorf("Cursor should be prompt-only (nil catalog), got %v", cmds)
	}
}
