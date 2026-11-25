package tmuxchrome

import "testing"

func TestIsNestedReturnCommand_RejectsLastWindow(t *testing.T) {
	if isNestedReturnCommand([]string{"last"}) {
		t.Error("`last` is tmux's alias for last-window, not switch-client -l")
	}
	if isNestedReturnCommand([]string{"last-session"}) {
		t.Error("tmux has no last-session command")
	}
	for _, cmd := range [][]string{
		{"switch-client", "-l"},
		{"switchc", "-l"},
		{"switch-client", "-ln"},
	} {
		if !isNestedReturnCommand(cmd) {
			t.Errorf("isNestedReturnCommand(%v) = false, want true", cmd)
		}
	}
}
