package conversations

import (
	"github.com/skzv/ccmux/internal/agent"
	"os"
	"path/filepath"
	"testing"
)

func TestMuseHistorySurface(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	id := "01a0828e-037e-7c32-bd87-87b6e25b70fd"
	path := filepath.Join(home, "data/muse/sessions/2026/09/08", id, "session.jsonl")
	data, err := os.ReadFile("../muse/testdata/native-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	all, err := ListMuse(home)
	if err != nil || len(all) != 1 {
		t.Fatalf("list %v: %v", all, err)
	}
	c := all[0]
	if c.Agent != agent.IDMuse || c.Project != "/projects/muse-demo" || c.Preview != "ccmux format probe" {
		t.Fatal(c)
	}
	args := c.ResumeArgsWithCommands(agent.Commands{Muse: "/custom/muse"})
	if len(args) != 3 || args[0] != "/custom/muse" || args[1] != "resume" || args[2] != id {
		t.Fatal(args)
	}
	if n, err := CountMessages(c); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	messages, err := RecentMessages(c, 1)
	if err != nil || len(messages) != 1 || messages[0].Role != "assistant" {
		t.Fatal(messages, err)
	}
}

func TestMatchesConversationQuery(t *testing.T) {
	c := Conversation{ID: "Muse-123", Project: "/projects/Русский", Preview: "Fix the cache reader"}
	for _, query := range []string{"", " MUSE-123 ", "русский", "CACHE"} {
		if !MatchesQuery(c, query) {
			t.Errorf("did not match %q", query)
		}
	}
	if MatchesQuery(c, "unrelated") {
		t.Fatal("matched unrelated query")
	}
}
