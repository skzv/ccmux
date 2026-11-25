package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestNotesSearchError_DoesNotTrapKeyboard(t *testing.T) {
	st := styles.Default()
	m := newNotes(st, DefaultKeymap())
	p := project.Project{Name: "demo", Path: t.TempDir()}
	m.project = &p
	m.searching = true
	m.searchInput.Blur()

	m, _ = m.applySearchResult(notesSearchResultMsg{
		Path: p.Path,
		Host: localDeviceLabel,
		Err:  "search timed out",
	})
	if m.searching {
		t.Fatal("m.searching remained true after search error — keyboard trapped")
	}
	if m.capturesInput() {
		t.Fatal("capturesInput() remained true after search error")
	}
}

func TestIdleSpinners_DoNotRearmWhenNotLoading(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()

	sm := newSettings(st, km, config.Defaults(), "dev")
	sm.SetMoshiState(moshi.Status{})
	if _, cmd := sm.Update(spinner.TickMsg{}); cmd != nil {
		t.Error("idle settingsModel re-armed spinner tick")
	}

	pm := newProjects(st, km)
	pm.SetProjects(nil) // marks loaded = true
	if _, cmd := pm.Update(spinner.TickMsg{}); cmd != nil {
		t.Error("loaded projectsModel re-armed spinner tick")
	}

	cm := newConversations(st, km)
	cm.SetLoading(false)
	if _, cmd := cm.Update(spinner.TickMsg{}); cmd != nil {
		t.Error("idle conversationsModel re-armed spinner tick")
	}
}

func TestSpawnBareSession_RemoteShellSendsShellTag(t *testing.T) {
	var got daemon.NewBareSessionRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sessions/bare" {
			_ = json.NewDecoder(r.Body).Decode(&got)
			_ = json.NewEncoder(w).Encode(daemon.NewBareSessionResponse{Session: "sh-1"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")

	cmd := spawnBareSessionCmd(newBareSessionSubmitMsg{
		Host:    "mini",
		Address: addr,
		Agent:   "", // sentinel for "shell (no agent)"
	})
	_ = cmd()
	if got.Agent != tmux.ShellAgentTag {
		t.Errorf("remote bare shell sent Agent=%q, want %q", got.Agent, tmux.ShellAgentTag)
	}
}

func TestNetworkModel_SetupSSHCmdStripsPortAndSetHostsPreservesSelection(t *testing.T) {
	st := styles.Default()
	m := newNetwork(st, DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "local", Local: true, Source: "local"},
		{Name: "mac-a", Key: "discovered://100.64.0.5:7474", Source: "discovered", Address: "100.64.0.5:7474", NeedsInstall: true},
		{Name: "mac-b", Key: "discovered://100.64.0.6:7474", Source: "discovered", Address: "100.64.0.6:7474", OK: true},
	})
	m.cursor = 1 // select mac-a
	cmd := m.SetupSSHCmd()
	if cmd == nil {
		t.Fatal("SetupSSHCmd returned nil for NeedsInstall peer")
	}
	msg := cmd()
	openMsg, ok := msg.(openSSHWizardMsg)
	if !ok {
		t.Fatalf("SetupSSHCmd returned %T, want openSSHWizardMsg", msg)
	}
	if openMsg.target.Host != "100.64.0.5" {
		t.Errorf("SSH target host = %q, want 100.64.0.5 (without :7474)", openMsg.target.Host)
	}

	// Reorder hosts — selection must stay pinned to mac-a by key.
	m.SetHosts([]hostStatus{
		{Name: "local", Local: true, Source: "local"},
		{Name: "mac-b", Key: "discovered://100.64.0.6:7474", Source: "discovered", Address: "100.64.0.6:7474", OK: true},
		{Name: "mac-a", Key: "discovered://100.64.0.5:7474", Source: "discovered", Address: "100.64.0.5:7474", NeedsInstall: true},
	})
	if sel := m.Selected(); sel == nil || sel.Name != "mac-a" {
		t.Errorf("after reorder Selected() = %+v, want mac-a", sel)
	}
}

func TestConversationPreviewOverlay_CachesRenderedMarkdown(t *testing.T) {
	st := styles.Default()
	var o conversationPreviewOverlay
	o.Open(conversations.Conversation{ID: "c1", Agent: agent.IDClaude, Project: "/tmp/demo"})
	o.SetMessages("c1", []conversations.Message{{Role: "user", Content: "hello **world**"}})
	v1 := o.View(st, 100, 30)
	if o.cache == nil || o.cache.body == "" {
		t.Fatal("expected preview cache to be populated after first View")
	}
	v2 := o.View(st, 100, 30)
	if v1 != v2 {
		t.Error("cached preview render differed from initial render")
	}
}

func TestClaudeMCPBrowser_ReadsUserClaudeJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	cfg := `{"mcpServers":{"ccmux":{"command":"ccmux-mcp","args":["--allow-mutate"]}}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	st := styles.Default()
	m := newClaude(st, DefaultKeymap())
	sec := m.browserMCPSection()
	if len(sec.Items) != 1 || sec.Items[0].Label != "ccmux" {
		t.Errorf("browserMCPSection() = %+v, want ccmux from .claude.json", sec.Items)
	}
}

func TestClockSkew_ClampsNegativeDuration(t *testing.T) {
	future := time.Now().Add(30 * time.Second)
	if got := relTime(future); strings.HasPrefix(got, "-") {
		t.Errorf("relTime(future) = %q, must not be negative", got)
	}
	if got := humanDuration(-15 * time.Second); got != "0s" {
		t.Errorf("humanDuration(-15s) = %q, want 0s", got)
	}
}
