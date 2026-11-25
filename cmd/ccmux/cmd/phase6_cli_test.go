package cmd

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/claudeconfig"
	"github.com/skzv/ccmux/internal/codexconfig"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
)

func TestProjectSetAgentCmd(t *testing.T) {
	home := withTempCcmuxConfig(t)
	projDir := filepath.Join(home, "Projects", "demo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	cfg.Projects.Root = filepath.Join(home, "Projects")
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	c := newProjectCmd()
	c.SetOut(&out)
	c.SetArgs([]string{"set-agent", "demo", "codex"})
	if err := c.Execute(); err != nil {
		t.Fatalf("project set-agent: %v", err)
	}
	if got := project.ReadAgent(projDir); got != agent.IDCodex {
		t.Errorf("ReadAgent = %q, want codex", got)
	}

	c2 := newProjectCmd()
	c2.SetArgs([]string{"set-agent", "demo", "not-an-agent"})
	if err := c2.Execute(); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestNotesNewCmd(t *testing.T) {
	home := withTempCcmuxConfig(t)
	projDir := filepath.Join(home, "Projects", "demo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	cfg.Projects.Root = filepath.Join(home, "Projects")
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	c := newNotesCmd()
	c.SetOut(&out)
	c.SetArgs([]string{"new", "demo", "docs/spec", "--title", "Spec Title"})
	if err := c.Execute(); err != nil {
		t.Fatalf("notes new: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(projDir, "docs", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# Spec Title\n\n" {
		t.Errorf("note body = %q", string(body))
	}
	// Duplicate fails.
	c2 := newNotesCmd()
	c2.SetArgs([]string{"new", "demo", "docs/spec.md"})
	if err := c2.Execute(); err == nil {
		t.Fatal("expected error on duplicate note")
	}
	// Remote notes new refused.
	c3 := newNotesCmd()
	c3.SetArgs([]string{"--host", "mini", "new", "demo", "docs/a.md"})
	if err := c3.Execute(); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("remote notes new err = %v, want read-only", err)
	}
}

func TestNewCmd_RemoteHost(t *testing.T) {
	withTempCcmuxConfig(t)
	origTTY := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = origTTY })

	var gotReq daemon.NewProjectRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/projects" {
			_ = json.NewDecoder(r.Body).Decode(&gotReq)
			_ = json.NewEncoder(w).Encode(daemon.NewProjectResponse{
				Session: "c-api", Path: "/remote/Projects/api", Host: "mini",
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	cfg, _ := config.Load()
	cfg.Hosts = []config.Host{{Name: "mini", Address: host, Port: port}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	c := newNewCmd()
	c.SetOut(&out)
	c.SetArgs([]string{"api", "--host", "mini", "--agent", "codex"})
	if err := c.Execute(); err != nil {
		t.Fatalf("new --host mini: %v", err)
	}
	if gotReq.Name != "api" || gotReq.Agent != "codex" {
		t.Errorf("POST /v1/projects body = %+v, want {Name:api Agent:codex}", gotReq)
	}
	if !strings.Contains(out.String(), "created c-api on mini") {
		t.Errorf("output = %q", out.String())
	}
}

func TestConfigGetSetListCmd(t *testing.T) {
	withTempCcmuxConfig(t)

	var out bytes.Buffer
	c := newConfigCmd()
	c.SetOut(&out)
	c.SetArgs([]string{"set", "sessions.attach_mode", "exclusive"})
	if err := c.Execute(); err != nil {
		t.Fatalf("config set: %v", err)
	}

	out.Reset()
	c2 := newConfigCmd()
	c2.SetOut(&out)
	c2.SetArgs([]string{"get", "sessions.attach_mode"})
	if err := c2.Execute(); err != nil {
		t.Fatalf("config get: %v", err)
	}
	if strings.TrimSpace(out.String()) != "exclusive" {
		t.Errorf("config get = %q, want exclusive", out.String())
	}

	c3 := newConfigCmd()
	c3.SetArgs([]string{"set", "sessions.attach_mode", "bogus"})
	if err := c3.Execute(); err == nil {
		t.Fatal("expected error for bogus attach_mode")
	}
}

func TestAgentsConfigSetAndShow(t *testing.T) {
	home := withTempCcmuxConfig(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))

	var out bytes.Buffer
	c := newAgentsCmd()
	c.SetOut(&out)
	c.SetArgs([]string{"config", "set", "claude", "effort", "high"})
	if err := c.Execute(); err != nil {
		t.Fatalf("agents config set claude effort: %v", err)
	}
	if eff, _ := claudeconfig.EffectiveEffortLevel(); eff != "high" {
		t.Errorf("claude effort = %q, want high", eff)
	}

	c2 := newAgentsCmd()
	c2.SetOut(&out)
	c2.SetArgs([]string{"config", "set", "codex", "yolo", "true"})
	if err := c2.Execute(); err != nil {
		t.Fatalf("agents config set codex yolo: %v", err)
	}
	if yolo, _ := codexconfig.EffectiveYoloMode(); !yolo {
		t.Errorf("codex yolo = false, want true")
	}

	out.Reset()
	c3 := newAgentsCmd()
	c3.SetOut(&out)
	c3.SetArgs([]string{"config", "claude", "--json"})
	if err := c3.Execute(); err != nil {
		t.Fatalf("agents config claude --json: %v", err)
	}
	var snaps []agentConfigSnapshot
	if err := json.Unmarshal(out.Bytes(), &snaps); err != nil || len(snaps) != 1 || snaps[0].Effort != "high" {
		t.Errorf("agents config json = %s (err=%v)", out.String(), err)
	}
}

func TestPreviewCmd_RemoteHost(t *testing.T) {
	withTempCcmuxConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sessions/c-demo/preview" {
			_ = json.NewEncoder(w).Encode(daemon.PreviewResponse{
				Lines:   10,
				Content: "line 1\nline 2\n",
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	cfg, _ := config.Load()
	cfg.Hosts = []config.Host{{Name: "mini", Address: host, Port: port}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	c := newPreviewCmd()
	c.SetOut(&out)
	c.SetArgs([]string{"c-demo", "--host", "mini", "--lines", "10"})
	if err := c.Execute(); err != nil {
		t.Fatalf("preview --host mini: %v", err)
	}
	if out.String() != "line 1\nline 2\n" {
		t.Errorf("preview output = %q", out.String())
	}
}

func TestReadConversationCmd(t *testing.T) {
	home := withTempCcmuxConfig(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	projDir := filepath.Join(home, "Projects", "demo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed a Claude JSONL transcript under ~/.claude/projects/<encoded>/<id>.jsonl
	encoded := strings.ReplaceAll(projDir, string(filepath.Separator), "-")
	tDir := filepath.Join(home, ".claude", "projects", encoded)
	if err := os.MkdirAll(tDir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "11111111-2222-4333-8444-555555555555"
	lines := strings.Join([]string{
		`{"type":"user","cwd":` + `"` + filepath.ToSlash(projDir) + `"` + `,"timestamp":"2026-11-24T10:00:00Z","message":{"role":"user","content":"Hello agent"}}`,
		`{"type":"assistant","cwd":` + `"` + filepath.ToSlash(projDir) + `"` + `,"timestamp":"2026-11-24T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Hi there!"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(tDir, id+".jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	c := newReadConversationCmd()
	c.SetOut(&out)
	c.SetArgs([]string{"11111111…", "--agent", "claude", "--json"})
	if err := c.Execute(); err != nil {
		t.Fatalf("read-conversation: %v", err)
	}
	if !strings.Contains(out.String(), "Hello agent") || !strings.Contains(out.String(), "Hi there!") {
		t.Errorf("read-conversation output missing turns: %s", out.String())
	}
}
