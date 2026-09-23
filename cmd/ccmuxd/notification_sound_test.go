package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestResolveMacSoundPath_SystemSoundName(t *testing.T) {
	base := "/System/Library/Sounds"
	exists := func(path string) bool {
		return path == filepath.Join(base, "Ping.aiff")
	}
	got, ok := resolveMacSoundPath("ping", base, exists)
	if !ok {
		t.Fatal("resolveMacSoundPath did not find Ping.aiff")
	}
	if want := filepath.Join(base, "Ping.aiff"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestResolveMacSoundPath_AbsolutePath(t *testing.T) {
	want := "/tmp/custom.aiff"
	got, ok := resolveMacSoundPath(want, "/System/Library/Sounds", func(path string) bool {
		return path == want
	})
	if !ok {
		t.Fatal("resolveMacSoundPath did not find absolute path")
	}
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

// TestStartDetached_OutlivesCallerContext — the bell is rung from a
// poll tick whose context is cancelled as soon as the tick returns; the
// sound player must keep running past that.
func TestStartDetached_OutlivesCallerContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	marker := filepath.Join(t.TempDir(), "played")
	orig := soundCommand
	t.Cleanup(func() { soundCommand = orig })
	soundCommand = func(ctx context.Context, path string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "sleep 0.3 && touch "+path)
	}

	tickCtx, cancelTick := context.WithCancel(context.Background())
	bell := func(ctx context.Context) error { return startDetached(marker) }
	if err := bell(tickCtx); err != nil {
		t.Fatal(err)
	}
	cancelTick() // pollOnce returning

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("sound player was killed before it finished")
}
