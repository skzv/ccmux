package conversations

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/codexconfig"
	"github.com/skzv/ccmux/internal/codexusage"
)

func TestCodexHome_ConfigConversationsUsageAndDeletion(t *testing.T) {
	home, codexRoot := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexRoot)
	dir := filepath.Join(codexRoot, "sessions", "2026", "10", "10")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	body := fmt.Sprintf("{\"timestamp\":%q,\"type\":\"session_meta\",\"payload\":{\"cwd\":\"/project\",\"originator\":\"codex-tui\"}}\n", ts)
	body += fmt.Sprintf("{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\",\"info\":{\"last_token_usage\":{\"input_tokens\":100,\"output_tokens\":20}}}}\n", ts)
	path := filepath.Join(dir, "rollout-2026-10-10T12-00-00-11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := codexconfig.Paths()
	if err != nil || paths.Root != codexRoot {
		t.Fatalf("configuration root: %v, %v", paths, err)
	}
	if c := readCodexTranscript(path); c.ID == "" {
		t.Fatal("invalid fixture")
	}
	convs, err := ListCodex(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 1 {
		t.Fatalf("config honors CODEX_HOME, but conversation listing found %d of 1 transcripts", len(convs))
	}
	agg, err := codexusage.Walk(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Total.Total() != 120 {
		t.Errorf("config honors CODEX_HOME, but usage = %d instead of 120", agg.Total.Total())
	}
	if got := (agent.Codex{}).TranscriptsRoot(home); got != filepath.Join(codexRoot, "sessions") {
		t.Errorf("agent transcript root = %s", got)
	}
	if err := Delete(convs[0]); err != nil {
		t.Fatalf("delete custom-root conversation: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("transcript remains after deletion: %v", err)
	}
	outside := filepath.Join(home, ".codex", "sessions", "rollout-outside.jsonl")
	writeFile(t, outside, body)
	if err := Delete(Conversation{Agent: agent.IDCodex, ID: "outside", Path: outside}); err == nil {
		t.Error("deleted outside the configured Codex root")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("outside transcript was changed: %v", err)
	}
}
