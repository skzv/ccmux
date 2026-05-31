package main

import (
	"path/filepath"
	"testing"
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
