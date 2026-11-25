package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
)

func TestHandleRegisterDevice_RejectsPreKeyOptions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	key := testEd25519AuthorizedKey(t)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "authorized_keys"), []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := daemon.OpenDeviceStore(filepath.Join(home, ".local", "state", "ccmux", "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := pairTestServer()
	s.devices = store

	// Pre-key option smuggling must be rejected (400) so it cannot mint
	// unlimited device entries for one authorized key.
	body, _ := json.Marshal(daemon.RegisterDeviceRequest{
		PublicKey: `from="1" ` + key,
		Token:     "tok-smuggled",
		Env:       "production",
	})
	rec := httptest.NewRecorder()
	s.handleRegisterDevice(rec, httptest.NewRequest(http.MethodPost, "/v1/devices", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	if len(store.All()) != 0 {
		t.Fatalf("store got %d entries, want 0", len(store.All()))
	}

	// Clean key with a comment is accepted and canonicalized.
	body, _ = json.Marshal(daemon.RegisterDeviceRequest{
		PublicKey: key + " my-phone",
		Token:     "tok-real",
		Env:       "production",
	})
	rec = httptest.NewRecorder()
	s.handleRegisterDevice(rec, httptest.NewRequest(http.MethodPost, "/v1/devices", bytes.NewReader(body)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if _, ok := store.Lookup(key); !ok {
		t.Fatal("registered key not found under canonical form")
	}
}
