package setupwizard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestStepSSHKey_HonorsContextDeadline — repo rule: every exec.Command
// carries a ctx. stepSSHKey used to run ssh-keygen via exec.Command,
// so a wedged binary hung the wizard forever. The step must return
// once the caller's context expires. Simulated with a fake ssh-keygen
// on PATH that sleeps far longer than the deadline (`exec` makes sleep
// replace the shell so the ctx kill lands on the pipe holder).
func TestStepSSHKey_HonorsContextDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-injected shell fake requires a Unix shell")
	}
	// Fresh HOME so ~/.ssh/id_ed25519 doesn't exist and the step
	// reaches the keygen call.
	t.Setenv("HOME", t.TempDir())

	bin := t.TempDir()
	script := filepath.Join(bin, "ssh-keygen")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// assume-yes answers the "Generate a new SSH key?" prompt without
	// a terminal; the 300ms deadline should kill the fake keygen.
	ctx, cancel := context.WithTimeout(withAssumeYes(context.Background()), 300*time.Millisecond)
	defer cancel()

	var out bytes.Buffer
	start := time.Now()
	err := stepSSHKey(ctx, &out)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stepSSHKey ignored the context deadline (took %v)", elapsed)
	}
	if err == nil {
		t.Fatal("expected an error from the killed ssh-keygen, got nil")
	}
}
