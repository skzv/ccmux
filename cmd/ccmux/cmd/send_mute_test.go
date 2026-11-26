package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

func TestSendAndMuteCLI_RemoteRouting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var sentKeys []string
	var mutedReq daemon.MuteRequest
	var sawMute bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sessions/c-api/send-keys":
			var req daemon.SendKeysRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			sentKeys = append(sentKeys, req.Keys)
			w.WriteHeader(http.StatusNoContent)
		case "/v1/sessions/c-api/mute":
			_ = json.NewDecoder(r.Body).Decode(&mutedReq)
			sawMute = true
			_ = json.NewEncoder(w).Encode(daemon.SessionState{Name: "c-api", Muted: mutedReq.Muted})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	hostPort := strings.TrimPrefix(srv.URL, "http://")
	hostOnly, portStr, _ := strings.Cut(hostPort, ":")
	cfg := config.Defaults()
	cfg.Hosts = []config.Host{{Name: "mini", Address: hostOnly}}
	var port int
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}
	cfg.Hosts[0].Port = port
	if err := config.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Test `ccmux send c-api yes please --host mini`
	sendCmd := newSendCmd()
	var out bytes.Buffer
	sendCmd.SetOut(&out)
	sendCmd.SetArgs([]string{"c-api", "yes", "please", "--host", "mini"})
	if err := sendCmd.Execute(); err != nil {
		t.Fatalf("send execute: %v", err)
	}
	if len(sentKeys) != 2 || sentKeys[0] != "yes please" || sentKeys[1] != "Enter" {
		t.Fatalf("sentKeys = %v, want [\"yes please\", \"Enter\"]", sentKeys)
	}

	// Test `ccmux mute c-api --host mini`
	muteCmd := newMuteCmd()
	out.Reset()
	muteCmd.SetOut(&out)
	muteCmd.SetArgs([]string{"c-api", "--host", "mini"})
	if err := muteCmd.Execute(); err != nil {
		t.Fatalf("mute execute: %v", err)
	}
	if !sawMute || !mutedReq.Muted {
		t.Fatalf("mute request = (%v, %+v), want (true, {Muted:true})", sawMute, mutedReq)
	}

	// Test `ccmux mute c-api --off --host mini`
	muteCmd2 := newMuteCmd()
	out.Reset()
	muteCmd2.SetOut(&out)
	muteCmd2.SetArgs([]string{"c-api", "--off", "--host", "mini"})
	if err := muteCmd2.Execute(); err != nil {
		t.Fatalf("unmute execute: %v", err)
	}
	if mutedReq.Muted {
		t.Fatalf("expected Muted=false on --off")
	}
}
