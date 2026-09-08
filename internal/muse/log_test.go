package muse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testID = "01a0828e-037e-7c32-bd87-87b6e25b70fd"

func fixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	path := filepath.Join(SessionsRoot(home), "2026", "09", "08", testID, "session.jsonl")
	data, err := os.ReadFile("testdata/native-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), ".session.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return home, path, data
}

func TestNativeLog(t *testing.T) {
	home, path, data := fixture(t)
	// Complete copied records and a partial trailing write must not duplicate
	// a user turn or discard the valid history preceding the partial write.
	if err := os.WriteFile(path, append(append(data, data...), []byte(`{"schema_version":`)...), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Workspace != "/projects/muse-demo" || s.ID != testID || len(s.Messages) != 2 || len(s.Requests) != 1 {
		t.Fatalf("unexpected session: %+v", s)
	}
	if s.Messages[0].Role != "user" || s.Messages[1].Content != "echo: ccmux format probe" {
		t.Fatal(s.Messages)
	}
	if s.LastActivity.Year() != 2026 {
		t.Fatal(s.LastActivity)
	}
	all, err := List(home)
	if err != nil || len(all) != 1 {
		t.Fatalf("list: %d %v", len(all), err)
	}
}

func TestChildUsageAndMirrors(t *testing.T) {
	_, path, data := fixture(t)
	var child []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if r["payload_type"] != "runtime.session" {
			continue
		}
		p := r["payload"].(map[string]any)
		event := p["event"].(map[string]any)
		if event["kind"] != "model_completed" {
			continue
		}
		r["id"] = "child-completion"
		p["source_run_record_id"] = "child-source-completion"
		event["usage"] = map[string]int{"input_tokens": 100, "output_tokens": 20, "cached_tokens": 10, "reasoning_tokens": 5}
		encoded, _ := json.Marshal(r)
		child = append(child, string(encoded))
		r["id"] = "mirrored-child-completion"
		encoded, _ = json.Marshal(r)
		child = append(child, string(encoded))
	}
	childPath := filepath.Join(filepath.Dir(path), "subagent", "worker", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(childPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childPath, []byte(strings.Join(child, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Messages) != 2 || len(s.Requests) != 2 || len(s.Paths) != 2 {
		t.Fatalf("messages=%d requests=%d paths=%d", len(s.Messages), len(s.Requests), len(s.Paths))
	}
	r := s.Requests[1]
	if r.Input != 100 || r.Output != 20 || r.Cached != 10 || r.Reasoning != 5 {
		t.Fatal(r)
	}
}

func TestXDGAndMissingLogs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if SessionsRoot(home) != filepath.Join(home, ".local/share/muse/sessions") {
		t.Fatal(SessionsRoot(home))
	}
	t.Setenv("XDG_DATA_HOME", "relative-is-invalid")
	if SessionsRoot(home) != filepath.Join(home, ".local/share/muse/sessions") {
		t.Fatal(SessionsRoot(home))
	}
	s, err := List(home)
	if err != nil || len(s) != 0 {
		t.Fatal(s, err)
	}
}

func TestDeleteNativeSession(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native Muse lock requires macOS/Linux")
	}
	home, path, _ := fixture(t)
	cache := filepath.Join(SessionsRoot(home), ".msp-view-v1", testID)
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "HEAD.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockSession(filepath.Join(filepath.Dir(path), ".session.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Delete(home, testID, path); err == nil {
		t.Fatal("deleted active session")
	}
	unlock()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("active data lost", err)
	}
	if err := Delete(home, testID, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("session directory retained", err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("cache retained", err)
	}
}

func TestDeleteRejectsLinksAndWrongIdentity(t *testing.T) {
	home, path, _ := fixture(t)
	if err := Delete(home, "not-the-session", path); err == nil {
		t.Fatal("accepted mismatched identity")
	}
	if err := Delete(home, testID, filepath.Join(t.TempDir(), "session.jsonl")); err == nil {
		t.Fatal("accepted outside path")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(path), "linked")); err != nil {
		t.Skip(err)
	}
	if err := Delete(home, testID, path); err == nil {
		t.Fatal("accepted session symlink")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("data removed", err)
	}
}

func TestDeleteProtectsActiveChild(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native Muse lock requires macOS/Linux")
	}
	home, path, _ := fixture(t)
	child := filepath.Join(filepath.Dir(path), "subagent", "worker", ".session.lock")
	if err := os.MkdirAll(filepath.Dir(child), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, nil, 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockSession(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := Delete(home, testID, path); err == nil {
		t.Fatal("deleted active child")
	}
	unlock()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("parent lost", err)
	}
	if _, err := os.Stat(child); err != nil {
		t.Fatal("child lost", err)
	}
	// A failed child lock must release the parent lock for a later retry.
	if err := Delete(home, testID, path); err != nil {
		t.Fatal(err)
	}
}
