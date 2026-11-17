//go:build unix

package proctree

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestSnapshot_FindsThisProcessAndItsChild — on the real ps: the test
// process is in the snapshot under its parent, and a child it starts
// shows in its descendants by name.
func TestSnapshot_FindsThisProcessAndItsChild(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("no ps on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, "sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })

	tab, err := Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	me, ok := tab.Lookup(os.Getpid())
	if !ok {
		t.Fatalf("this process (%d) is not in the snapshot of %d", os.Getpid(), tab.Len())
	}
	if me.PPID != os.Getppid() {
		t.Errorf("PPID = %d, want %d", me.PPID, os.Getppid())
	}
	found := false
	for _, p := range tab.Descendants(os.Getpid()) {
		if p.PID == child.Process.Pid {
			found = true
			if p.Name != "sleep" {
				t.Errorf("child's name = %q, want sleep", p.Name)
			}
		}
	}
	if !found {
		t.Errorf("child %d not among this process's descendants", child.Process.Pid)
	}
}
