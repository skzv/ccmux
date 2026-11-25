package clipboard

import (
	"errors"
	"reflect"
	"testing"
)

func TestLinuxClipboardToolSelection(t *testing.T) {
	allInstalled := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	onlyXclip := func(name string) (string, error) {
		if name == "xclip" {
			return "/usr/bin/xclip", nil
		}
		return "", errors.New("not found")
	}

	if got := linuxClipboardTool(false, true, allInstalled); !reflect.DeepEqual(got, []string{"xclip", "-selection", "clipboard"}) {
		t.Errorf("X11 with wl-copy+xclip installed = %v, want xclip", got)
	}
	if got := linuxClipboardTool(true, true, allInstalled); !reflect.DeepEqual(got, []string{"wl-copy"}) {
		t.Errorf("Wayland+XWayland = %v, want wl-copy", got)
	}
	if got := linuxClipboardTool(false, false, allInstalled); !reflect.DeepEqual(got, []string{"wl-copy"}) {
		t.Errorf("headless fallback (wl-copy present) = %v, want wl-copy", got)
	}
	if got := linuxClipboardTool(false, false, onlyXclip); !reflect.DeepEqual(got, []string{"xclip", "-selection", "clipboard"}) {
		t.Errorf("headless fallback (only xclip) = %v, want xclip", got)
	}
}

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
