package remoteattach

import (
	"reflect"
	"testing"
)

// TestSSH_Argv pins the argv shape that ends up in front of the user's
// remote shell. A drift in the -t flag or the ordering would silently
// break the TUI's remote attach.
func TestSSH_Argv(t *testing.T) {
	cmd := SSH("alice@mac-mini", "tmux attach-session -t c-foo")
	want := []string{"ssh", "-t", "alice@mac-mini", "tmux attach-session -t c-foo"}
	got := append([]string{cmd.Path}, cmd.Args[1:]...)
	got[0] = "ssh" // Path is the absolute resolution; compare the basename concept
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SSH argv = %v, want %v", got, want)
	}
}

// TestSSHInteractive_Argv — no remote command, just a login shell.
func TestSSHInteractive_Argv(t *testing.T) {
	cmd := SSHInteractive("alice@mac-mini")
	if len(cmd.Args) != 3 {
		t.Fatalf("argv len = %d, want 3 (ssh -t target)", len(cmd.Args))
	}
	if cmd.Args[1] != "-t" {
		t.Errorf("expected -t flag, got %v", cmd.Args)
	}
}

// TestMosh_Argv — mosh needs `-- bash -c <cmd>` to take the same
// "string with shell metachars" remoteCmd that ssh accepts directly.
func TestMosh_Argv(t *testing.T) {
	cmd := Mosh("alice@mac-mini", "tmux attach-session -t c-foo")
	if cmd.Args[1] != "alice@mac-mini" {
		t.Errorf("target should be argv[1]; got %v", cmd.Args)
	}
	if cmd.Args[2] != "--" {
		t.Errorf("expected '--' separator at argv[2]; got %v", cmd.Args)
	}
	if cmd.Args[3] != "bash" || cmd.Args[4] != "-c" {
		t.Errorf("expected 'bash -c' after separator; got %v", cmd.Args)
	}
	if cmd.Args[5] != "tmux attach-session -t c-foo" {
		t.Errorf("remoteCmd should pass through verbatim; got %q", cmd.Args[5])
	}
}

// TestRunArgv_PicksBinary — useMosh=true → mosh, false → ssh.
func TestRunArgv_PicksBinary(t *testing.T) {
	ssh := RunArgv("alice@mini", false, []string{"tmux", "attach", "-t", "c-foo"})
	if ssh.Args[0] != "ssh" {
		t.Errorf("useMosh=false should use ssh; got %v", ssh.Args)
	}
	mosh := RunArgv("alice@mini", true, []string{"tmux", "attach", "-t", "c-foo"})
	if mosh.Args[0] != "mosh" {
		t.Errorf("useMosh=true should use mosh; got %v", mosh.Args)
	}
	// Both interpose `--` before the argv so the remote shell parses
	// the remaining tokens as a single command.
	for _, c := range []string{"ssh", "mosh"} {
		var cmd = ssh
		if c == "mosh" {
			cmd = mosh
		}
		if cmd.Args[2] != "--" {
			t.Errorf("%s argv should have -- at index 2; got %v", c, cmd.Args)
		}
	}
}
