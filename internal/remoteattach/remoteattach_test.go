package remoteattach

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// TestSSH_Argv pins the argv shape that ends up in front of the user's
// remote shell. A drift in the -t flag or the ordering would silently
// break the TUI's remote attach.
func TestSSH_Argv(t *testing.T) {
	cmd := SSH("alice@mac-mini", "tmux attach-session -t c-foo", 0)
	want := []string{"ssh", "-t", "alice@mac-mini", "tmux attach-session -t c-foo"}
	got := append([]string{cmd.Path}, cmd.Args[1:]...)
	got[0] = "ssh" // Path is the absolute resolution; compare the basename concept
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SSH argv = %v, want %v", got, want)
	}
}

// TestSSHInteractive_Argv — no remote command, just a login shell.
func TestSSHInteractive_Argv(t *testing.T) {
	cmd := SSHInteractive("alice@mac-mini", 0)
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
	cmd := Mosh("alice@mac-mini", "tmux attach-session -t c-foo", 0)
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
	ssh := RunArgv("alice@mini", false, 0, []string{"tmux", "attach", "-t", "c-foo"})
	if ssh.Args[0] != "ssh" {
		t.Errorf("useMosh=false should use ssh; got %v", ssh.Args)
	}
	mosh := RunArgv("alice@mini", true, 0, []string{"tmux", "attach", "-t", "c-foo"})
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

// TestSSH_CustomPortAddsFlag — a configured ssh_port must reach the
// argv. This was the last gap in the port chain: the TUI threaded
// SSHPort through the create-session forms, but the attach that
// followed built `ssh -t host` and dialed 22 anyway, so a session
// created on a custom-port host stranded the user.
func TestSSH_CustomPortAddsFlag(t *testing.T) {
	cmd := SSH("alice@mac-mini", "tmux attach -t c-foo", 2222)
	want := []string{"ssh", "-t", "-p", "2222", "alice@mac-mini", "tmux attach -t c-foo"}
	got := append([]string{"ssh"}, cmd.Args[1:]...)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SSH argv = %v, want %v", got, want)
	}
}

// TestDefaultPorts_ArgvUnchanged — 0 (unset) and 22 must produce argv
// byte-identical to the no-port form. The overwhelmingly common case
// can't regress just because the parameter exists.
func TestDefaultPorts_ArgvUnchanged(t *testing.T) {
	for _, port := range []int{0, 22} {
		ssh := SSH("mini", "cmd", port)
		if want := []string{"ssh", "-t", "mini", "cmd"}; !reflect.DeepEqual(append([]string{"ssh"}, ssh.Args[1:]...), want) {
			t.Errorf("SSH(port=%d) argv = %v, want %v", port, ssh.Args, want)
		}
		inter := SSHInteractive("mini", port)
		if want := []string{"ssh", "-t", "mini"}; !reflect.DeepEqual(append([]string{"ssh"}, inter.Args[1:]...), want) {
			t.Errorf("SSHInteractive(port=%d) argv = %v, want %v", port, inter.Args, want)
		}
		mosh := Mosh("mini", "cmd", port)
		for _, a := range mosh.Args {
			if strings.HasPrefix(a, "--ssh") {
				t.Errorf("Mosh(port=%d) should not add --ssh; got %v", port, mosh.Args)
			}
		}
	}
}

// TestMosh_CustomPortUsesSSHFlagNotDashP — the trap worth a test of its
// own. mosh's own -p is the UDP port range for the mosh session, NOT
// the SSH port; passing `-p 2222` the way ssh takes it would ask for a
// UDP bind on 2222 while still connecting to SSH on 22. The SSH port
// has to travel via --ssh.
func TestMosh_CustomPortUsesSSHFlagNotDashP(t *testing.T) {
	cmd := Mosh("alice@mini", "tmux attach -t c-foo", 2222)
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--ssh=ssh -p 2222") {
		t.Errorf("mosh should carry the ssh port via --ssh; got %v", cmd.Args)
	}
	for i, a := range cmd.Args {
		if a == "-p" {
			t.Errorf("argv[%d] is a bare -p — that's mosh's UDP port range, not the ssh port: %v", i, cmd.Args)
		}
	}
	// The target and command shape must survive the added flag.
	if cmd.Args[len(cmd.Args)-3] != "bash" || cmd.Args[len(cmd.Args)-2] != "-c" {
		t.Errorf("expected trailing `bash -c <cmd>`; got %v", cmd.Args)
	}
}

// TestRunArgv_PortPerBinary — RunArgv picks the port syntax matching
// whichever binary it picked.
func TestRunArgv_PortPerBinary(t *testing.T) {
	argv := []string{"tmux", "attach", "-t", "c-foo"}

	ssh := RunArgv("mini", false, 2222, argv)
	if got := strings.Join(ssh.Args, " "); !strings.Contains(got, "-p 2222") {
		t.Errorf("ssh RunArgv should carry -p 2222; got %v", ssh.Args)
	}
	mosh := RunArgv("mini", true, 2222, argv)
	if got := strings.Join(mosh.Args, " "); !strings.Contains(got, "--ssh=ssh -p 2222") {
		t.Errorf("mosh RunArgv should carry --ssh; got %v", mosh.Args)
	}
	// `--` still separates the remote argv in both.
	for _, cmd := range []*exec.Cmd{ssh, mosh} {
		sawSep := false
		for _, a := range cmd.Args {
			if a == "--" {
				sawSep = true
			}
		}
		if !sawSep {
			t.Errorf("missing -- separator: %v", cmd.Args)
		}
	}
}
