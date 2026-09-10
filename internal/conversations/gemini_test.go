package conversations

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

func TestGeminiHistoryOwnershipAndResume(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "work", "demo")
	dir := filepath.Join(home, ".gemini", "tmp", "demo")
	writeFile(t, filepath.Join(dir, ".project_root"), project)
	legacy := filepath.Join(dir, "chats", "session-one.json")
	writeFile(t, legacy, `{"sessionId":"native-id","projectHash":"hash","messages":[{"type":"user","content":"legacy"}]}`)
	writeFile(t, legacy+"l", `{"sessionId":"native-id","projectHash":"hash"}
{"id":"u1","type":"user","content":[{"text":"fresh preview"}]}
{"id":"g1","type":"gemini","content":"answer"}
`)
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-cli", "conversations", "agy-id.pb"), "opaque")
	all, err := All(Options{HomeDir: home})
	if err != nil || len(all) != 2 {
		t.Fatal(all, err)
	}
	var c Conversation
	for _, row := range all {
		if row.Agent == agent.IDGemini {
			c = row
		}
	}
	if c.ID != "native-id" || c.Project != project || c.Preview != "fresh preview" || len(c.Paths) != 2 {
		t.Fatalf("%+v", c)
	}
	if err := c.ValidateResume(); err != nil {
		t.Fatal(err)
	}
	want := []string{"/custom/gemini", "--resume", "native-id"}
	if got := c.ResumeArgsWithCommands(agent.Commands{Gemini: "/custom/gemini"}); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if n, err := CountMessages(c); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if msgs, err := RecentMessages(c, 5); err != nil || len(msgs) != 2 {
		t.Fatal(msgs, err)
	}
	c.Project = "project hash"
	if err := c.ValidateResume(); err == nil {
		t.Fatal("accepted a fabricated cwd")
	}
	if err := guardTranscriptPath(home, agent.IDAntigravity, legacy); err == nil {
		t.Fatal("Antigravity can delete Gemini history")
	}
}
