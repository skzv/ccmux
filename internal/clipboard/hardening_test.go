package clipboard

import "testing"

func TestEnvMarksRemote_NoSubstringFalsePositives(t *testing.T) {
	for _, local := range []string{
		"PATH=/usr/bin\x00SSH_CONNECTION=\x00HOME=/home/u",
		"PATH=/usr/bin\x00FOO_SSH_CONNECTION=1.2.3.4\x00",
		"PATH=/usr/bin\x00CCMUX_REMOTE_CLIENT=10\x00",
		"1234 ttys001 0:00.01 tmux FOO_SSH_CONNECTION=1 CCMUX_REMOTE_CLIENT=10",
	} {
		if envMarksRemote([]byte(local)) {
			t.Errorf("envMarksRemote(%q) = true, want false", local)
		}
	}
	for _, remote := range []string{
		"PATH=/usr/bin\x00SSH_CONNECTION=1.2.3.4 55555 5.6.7.8 22\x00",
		"PATH=/usr/bin\x00CCMUX_REMOTE_CLIENT=1\x00",
		"1234 ttys001 0:00.01 tmux SSH_CONNECTION=1.2.3.4",
		"1234 ttys001 0:00.01 tmux CCMUX_REMOTE_CLIENT=1",
	} {
		if !envMarksRemote([]byte(remote)) {
			t.Errorf("envMarksRemote(%q) = false, want true", remote)
		}
	}
}
