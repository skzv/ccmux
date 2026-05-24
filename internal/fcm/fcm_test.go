package fcm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew_DisabledIsNoOp(t *testing.T) {
	s, err := New(Config{Enabled: false})
	if err != nil {
		t.Fatalf("New(disabled): %v", err)
	}
	if s == nil {
		t.Fatal("New(disabled) returned nil sender")
	}
	if s.Enabled() {
		t.Fatal("disabled sender reports Enabled()")
	}
	// Send must be a quiet no-op so callers can compose dispatch
	// loops without nil-checking.
	if err := s.Send("ignored", Notification{}); err != nil {
		t.Fatalf("Send on disabled: %v", err)
	}
}

func TestNew_EnabledWithoutCredentialsErrors(t *testing.T) {
	_, err := New(Config{
		Enabled:         true,
		CredentialsPath: "/nonexistent/path/firebase.json",
		ProjectID:       "ccmux-mobile",
	})
	if err == nil {
		t.Fatal("expected error when credentials path is missing")
	}
	if !strings.Contains(err.Error(), "read credentials") {
		t.Fatalf("expected read-credentials error, got: %v", err)
	}
}

func TestNew_EnabledWithoutProjectIDErrors(t *testing.T) {
	dir := t.TempDir()
	cred := filepath.Join(dir, "fcm.json")
	if err := os.WriteFile(cred, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{
		Enabled:         true,
		CredentialsPath: cred,
		ProjectID:       "  ",
	})
	if err == nil {
		t.Fatal("expected error when ProjectID is empty")
	}
	if !strings.Contains(err.Error(), "ProjectID required") {
		t.Fatalf("expected ProjectID-required error, got: %v", err)
	}
}

func TestNew_EnabledWithCredentialsSucceeds(t *testing.T) {
	dir := t.TempDir()
	cred := filepath.Join(dir, "fcm.json")
	if err := os.WriteFile(cred, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Enabled:         true,
		CredentialsPath: cred,
		ProjectID:       "ccmux-mobile",
	})
	if err != nil {
		t.Fatalf("New(enabled): %v", err)
	}
	if !s.Enabled() {
		t.Fatal("enabled sender reports !Enabled()")
	}
	// Dormant package: real send goes through a future real-sender
	// PR. For now, Send with a valid-looking token returns nil and
	// an empty token is rejected so misbehaving callers can't no-op
	// their own bugs.
	if err := s.Send("placeholder-fcm-token", Notification{
		Title:     "test",
		Body:      "test",
		SessionID: "local/c-foo",
		Kind:      "needs_input",
		Host:      "mini",
	}); err != nil {
		t.Fatalf("Send dormant: %v", err)
	}
	if err := s.Send("", Notification{}); err == nil {
		t.Fatal("expected error on empty token")
	}
}

func TestNil_Sender(t *testing.T) {
	var s *Sender
	if s.Enabled() {
		t.Fatal("nil sender reports Enabled()")
	}
	if err := s.Send("anything", Notification{}); err != nil {
		t.Fatalf("nil Send: %v", err)
	}
}
