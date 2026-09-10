package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestGeminiIndependentIdentityAndCommands(t *testing.T) {
	for _, name := range []string{"gemini", " GEMINI "} {
		id, ok := ParseID(name)
		if !ok || id != IDGemini || ByID(id).Binary() != "gemini" {
			t.Fatal(id, ok)
		}
	}
	commands := Commands{Gemini: "/tmp/Gemini CLI/gemini", Antigravity: "/tmp/agy"}
	if got := LaunchCmd(IDGemini, false, commands); got != "'/tmp/Gemini CLI/gemini'" {
		t.Fatal(got)
	}
	if got := LaunchCmd(IDGemini, true, commands); got != "'/tmp/Gemini CLI/gemini' --resume || '/tmp/Gemini CLI/gemini' || zsh || bash || sh" {
		t.Fatal(got)
	}
	if got := ResumeArgs(IDGemini, "native-id", commands); !reflect.DeepEqual(got, []string{"/tmp/Gemini CLI/gemini", "--resume", "native-id"}) {
		t.Fatal(got)
	}
	if got := ResumeArgs(IDAntigravity, "agy-id", commands); !reflect.DeepEqual(got, []string{"/tmp/agy", "--conversation", "agy-id"}) {
		t.Fatal(got)
	}
	if strings.Contains(Gemini{}.InitialPrompt("demo", ""), "write AGENTS.md") {
		t.Fatal("Gemini must request GEMINI.md")
	}
}

func TestGeminiInstalledWithoutAntigravity(t *testing.T) {
	old := installLookupHook
	t.Cleanup(func() { installLookupHook = old })
	installLookupHook = func(_ context.Context, binary string) bool { return binary == "gemini" }
	got := AllInstalled(context.Background())
	if len(got) != 1 || got[0].ID() != IDGemini {
		t.Fatal(got)
	}
}
