//go:build !windows

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runRouted executes argv with exactly env (plus a PATH that has sh)
// and returns combined output + whether it exited cleanly.
func runRouted(t *testing.T, argv []string, env ...string) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}, env...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("routed launch hung: %s", out)
	}
	return string(out), err == nil
}

// TestOpenRouterLaunch_NeverExportsOpenAIKey runs the generated launch
// and resume commands under /bin/sh. With only OPENAI_API_KEY in the
// environment, the old `${OPENROUTER_API_KEY:-$OPENAI_API_KEY}` export
// handed the user's OpenAI key to the agent as its OpenRouter
// credential — sending it to openrouter.ai / the configured base_url.
// Now the agent must not start at all, and the key must not surface.
func TestOpenRouterLaunch_NeverExportsOpenAIKey(t *testing.T) {
	const openAIKey = "sk-openai-REAL-SECRET"
	const routerKey = "sk-or-test-key"
	dir := t.TempDir()
	fake := filepath.Join(dir, "codex")
	script := "#!/bin/sh\necho \"agent-ran key=$OPENAI_API_KEY base=$OPENAI_BASE_URL args=$*\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmds := Commands{Codex: fake, OpenRouterAgents: map[ID]bool{IDCodex: true}}

	launches := map[string][]string{
		"launch":   {"/bin/sh", "-c", LaunchCmd(IDCodex, false, cmds)},
		"continue": {"/bin/sh", "-c", LaunchCmd(IDCodex, true, cmds)},
		"resume":   ResumeArgs(IDCodex, "abc-123", cmds),
	}
	for name, argv := range launches {
		t.Run(name+"/openai key only", func(t *testing.T) {
			out, ok := runRouted(t, argv, "OPENAI_API_KEY="+openAIKey)
			if strings.Contains(out, openAIKey) {
				t.Fatalf("OpenAI key reached the routed agent:\n%s", out)
			}
			if strings.Contains(out, "agent-ran") {
				t.Errorf("agent started without OPENROUTER_API_KEY:\n%s", out)
			}
			if ok {
				t.Errorf("launch without OPENROUTER_API_KEY must exit non-zero:\n%s", out)
			}
			if !strings.Contains(out, "OPENROUTER_API_KEY is not set") {
				t.Errorf("missing explanation for the refused launch:\n%s", out)
			}
		})
		t.Run(name+"/empty router key", func(t *testing.T) {
			out, _ := runRouted(t, argv, "OPENAI_API_KEY="+openAIKey, "OPENROUTER_API_KEY=")
			if strings.Contains(out, openAIKey) || strings.Contains(out, "agent-ran") {
				t.Fatalf("empty OPENROUTER_API_KEY must not launch with the OpenAI key:\n%s", out)
			}
		})
		t.Run(name+"/router key set", func(t *testing.T) {
			out, ok := runRouted(t, argv, "OPENAI_API_KEY="+openAIKey, "OPENROUTER_API_KEY="+routerKey)
			if !ok {
				t.Fatalf("routed launch failed:\n%s", out)
			}
			if !strings.Contains(out, "agent-ran key="+routerKey+" base=https://openrouter.ai/api/v1") {
				t.Errorf("agent did not get the OpenRouter credential + base URL:\n%s", out)
			}
			if strings.Contains(out, openAIKey) {
				t.Errorf("OpenAI key reached the routed agent:\n%s", out)
			}
		})
	}
}
