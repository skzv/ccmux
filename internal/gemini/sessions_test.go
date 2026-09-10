package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadJSONLUpdatesRewindsAndParts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-test.jsonl")
	writeFixture(t, path, `{"sessionId":"s1","projectHash":"hash","startTime":"2026-09-09T00:00:00Z"}
{"id":"u1","type":"user","content":[{"text":"Fix "},{"text":"the bug"},{"inlineData":{"data":"not preview text"}}],"timestamp":"2026-09-09T00:00:01Z"}
{"id":"g1","type":"gemini","content":"Working","tokens":{"input":10,"output":1}}
{"id":"g1","type":"gemini","content":"Done","tokens":{"input":20,"output":5}}
{"id":"u2","type":"user","content":"Discard this"}
{"id":"g2","type":"gemini","content":"Discard this too"}
{"$rewindTo":"u2"}
{"$set":{"lastUpdated":"2026-09-09T00:02:00Z"}}
{"partial":`)
	s, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Messages) != 2 || s.Messages[0].Text() != "Fix the bug" || s.Messages[1].Tokens.Input != 20 {
		t.Fatalf("%+v", s)
	}
	if s.Updated.Minute() != 2 {
		t.Fatal(s.Updated)
	}
	writeFixture(t, path, `{"sessionId":"s1","projectHash":"hash"}
{"id":"old","type":"user","content":"old"}
{"$set":{"messages":[{"id":"new","type":"user","content":"checkpoint"}]}}
`)
	s, err = Read(path)
	if err != nil || len(s.Messages) != 1 || s.Messages[0].ID != "new" {
		t.Fatal(s, err)
	}
}

func TestListResolvesProjectsAndPrefersMigratedJSONL(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "work", "demo")
	registry, _ := json.Marshal(map[string]any{"projects": map[string]string{project: "demo"}})
	writeFixture(t, filepath.Join(ConfigRoot(home), "projects.json"), string(registry))
	base := filepath.Join(SessionsRoot(home), "demo", "chats", "session-test.json")
	writeFixture(t, base, `{"sessionId":"s1","projectHash":"hash","messages":[{"type":"user","content":"legacy"}]}`)
	writeFixture(t, base+"l", "{\"sessionId\":\"s1\",\"projectHash\":\"hash\"}\n{\"id\":\"u1\",\"type\":\"user\",\"content\":\"current\"}\n")
	writeFixture(t, filepath.Join(ConfigRoot(home), "antigravity-cli", "conversations", "native.pb"), "agy")
	writeFixture(t, filepath.Join(ConfigRoot(home), "settings.json"), "{}")
	writeFixture(t, filepath.Join(SessionsRoot(home), "demo", "chats", "broken.json"), "{")
	got, err := List(home)
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	if got[0].Project != project || got[0].Path != base+"l" || len(got[0].Paths) != 2 || got[0].Messages[0].Text() != "current" {
		t.Fatalf("%+v", got[0])
	}
	// Native ownership markers also work if the central registry is absent.
	if err := os.Remove(filepath.Join(ConfigRoot(home), "projects.json")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(SessionsRoot(home), "demo", ".project_root"), project+"\n")
	got, err = List(home)
	if err != nil || got[0].Project != project {
		t.Fatal(got, err)
	}
	for _, path := range got[0].Paths {
		if err := ValidateDelete(home, "s1", path); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateDelete(home, "wrong-id", base); err == nil {
		t.Fatal("accepted wrong ID")
	}
	if err := ValidateDelete(home, "s1", filepath.Join(ConfigRoot(home), "settings.json")); err == nil {
		t.Fatal("accepted settings file")
	}
}

func TestDeleteRejectsSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.json")
	writeFixture(t, outside, `{"sessionId":"s1","projectHash":"hash"}`)
	path := filepath.Join(SessionsRoot(home), "demo", "chats", "session-link.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skip(err)
	}
	if err := ValidateDelete(home, "s1", path); err == nil {
		t.Fatal("accepted symlink escape")
	}
	got, err := List(home)
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}
