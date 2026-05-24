package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const testPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx test@phone"

// TestOpenDeviceStore_FreshCreatesEmptyStore — opening a non-existent
// path is the "first run" case and must return a usable store without
// touching disk yet. The store is empty.
func TestOpenDeviceStore_FreshCreatesEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatalf("OpenDeviceStore: %v", err)
	}
	if got := len(s.All()); got != 0 {
		t.Errorf("fresh store has %d entries, want 0", got)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("fresh store should not write devices.json until first Register")
	}
}

// TestOpenDeviceStore_LoadsExisting — re-opening a store containing
// prior registrations restores them all and keys them by public-key
// hash (not by the raw key).
func TestOpenDeviceStore_LoadsExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Register(testPubKey, "TOK1", "production"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	reopened, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Lookup(testPubKey)
	if !ok {
		t.Fatal("re-opened store lost the prior registration")
	}
	if got.Token != "TOK1" || got.Environment != "production" {
		t.Errorf("got %+v after reload", got)
	}
}

// TestRegister_RefreshOverwritesSameKey — iOS rolls tokens periodically;
// a re-Register with the same key must replace the entry rather than
// appending a duplicate, so push routing always targets the live token.
func TestRegister_RefreshOverwritesSameKey(t *testing.T) {
	s, err := OpenDeviceStore(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Register(testPubKey, "OLD-TOKEN", "development"); err != nil {
		t.Fatal(err)
	}
	if err := s.Register(testPubKey, "NEW-TOKEN", "production"); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Lookup(testPubKey)
	if !ok {
		t.Fatal("Lookup miss after refresh")
	}
	if got.Token != "NEW-TOKEN" || got.Environment != "production" {
		t.Errorf("after refresh got %+v, want NEW-TOKEN/production", got)
	}
	if n := len(s.All()); n != 1 {
		t.Errorf("All() = %d entries, want 1 (refresh must not duplicate)", n)
	}
}

// TestRegister_RejectsInvalidEnv — env outside {development, production}
// must be refused so a malformed phone request can't store a value
// that breaks the daemon's per-env client routing later.
func TestRegister_RejectsInvalidEnv(t *testing.T) {
	s, err := OpenDeviceStore(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{"", "prod", "PROD", "Production", "sandbox"}
	for _, env := range cases {
		t.Run("env="+env, func(t *testing.T) {
			if err := s.Register(testPubKey, "TOK", env); err == nil {
				t.Errorf("Register accepted env=%q", env)
			}
		})
	}
}

// TestRegister_RejectsEmptyFields — empty public key or token are
// rejected: storing junk would only mean pushing to junk.
func TestRegister_RejectsEmptyFields(t *testing.T) {
	s, err := OpenDeviceStore(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Register("", "TOK", "production"); err == nil {
		t.Error("Register accepted empty public key")
	}
	if err := s.Register("   ", "TOK", "production"); err == nil {
		t.Error("Register accepted whitespace public key")
	}
	if err := s.Register(testPubKey, "", "production"); err == nil {
		t.Error("Register accepted empty token")
	}
}

// TestLookup_MissReturnsZero — a public key with no registration
// returns the zero value + false, never a stale entry.
func TestLookup_MissReturnsZero(t *testing.T) {
	s, err := OpenDeviceStore(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s.Lookup("ssh-ed25519 unknown")
	if ok {
		t.Errorf("Lookup unexpectedly found: %+v", got)
	}
	if got != (DeviceRegistration{}) {
		t.Errorf("miss returned non-zero value: %+v", got)
	}
}

// TestRemove_Idempotent — Remove must succeed on a missing entry (the
// daemon Removes when APNs reports the token is dead; it shouldn't have
// to first check Lookup).
func TestRemove_Idempotent(t *testing.T) {
	s, err := OpenDeviceStore(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("ssh-ed25519 not-registered"); err != nil {
		t.Errorf("Remove on missing entry: %v, want nil", err)
	}
	if err := s.Register(testPubKey, "TOK", "production"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(testPubKey); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := s.Lookup(testPubKey); ok {
		t.Error("entry survived Remove")
	}
}

// TestHashPublicKey_NormalizesComment — keys that differ only in
// trailing comment / whitespace must hash identically so a re-pair
// with the same key body doesn't accidentally create a new slot.
func TestHashPublicKey_NormalizesComment(t *testing.T) {
	base := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIxxxxxxx"
	cases := []string{
		base,
		base + " phone@home",
		"  " + base + "  laptop@office  \n",
	}
	first := HashPublicKey(cases[0])
	for _, c := range cases[1:] {
		if got := HashPublicKey(c); got != first {
			t.Errorf("HashPublicKey(%q) = %q, want %q (comments/whitespace must normalize)", c, got, first)
		}
	}
}

// TestFlush_AtomicWrite — Register's flush goes through write-then-
// rename so a crash mid-write can't truncate devices.json. The
// implementation uses .tmp + Rename; this test confirms no .tmp file
// lingers after a successful Register.
func TestFlush_AtomicWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Register(testPubKey, "TOK", "production"); err != nil {
		t.Fatal(err)
	}
	// The temp file is in the same directory; confirm it's gone.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "devices.json" {
			t.Errorf("leftover file %q after Register (expected only devices.json)", e.Name())
		}
	}
	// And the actual file content is valid JSON.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var list []DeviceRegistration
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("devices.json is not valid JSON: %v\n%s", err, raw)
	}
	if len(list) != 1 || list[0].Token != "TOK" {
		t.Errorf("on-disk list = %+v", list)
	}
}

// TestRegister_Concurrent — many simultaneous registrations must not
// corrupt the store (run under -race to make goroutine interleavings
// matter). Each iteration uses a distinct key so they don't merge.
func TestRegister_Concurrent(t *testing.T) {
	s, err := OpenDeviceStore(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI" + string(rune('a'+i))
			if err := s.Register(key, "TOK", "production"); err != nil {
				t.Errorf("Register: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := len(s.All()); n != N {
		t.Errorf("after concurrent register: %d entries, want %d", n, N)
	}
}

// TestRegister_LegacyAPNsRoundtrip — the legacy Register(publicKey,
// token, env) call must still produce an APNs-provider record so
// existing iOS clients keep working unchanged after the multi-
// provider migration. The new code path delegates to
// RegisterWithProvider(..., ProviderAPNs, env) so Provider is
// persisted explicitly; the on-disk migration from records that
// predate the field is covered by TestOpenDeviceStore_MigratesLegacyJSON.
func TestRegister_LegacyAPNsRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Register(testPubKey, "ios-tok", "production"); err != nil {
		t.Fatalf("legacy Register: %v", err)
	}
	regs := s.All()
	if len(regs) != 1 {
		t.Fatalf("want 1 reg, got %d", len(regs))
	}
	got := regs[0]
	if got.ResolvedProvider() != ProviderAPNs {
		t.Errorf("ResolvedProvider=%q, want %q", got.ResolvedProvider(), ProviderAPNs)
	}
	if got.Environment != "production" {
		t.Errorf("Environment=%q, want \"production\"", got.Environment)
	}
}

// TestRegisterWithProvider_FCM — an FCM registration has no env and
// resolves to provider "fcm" without any migration step.
func TestRegisterWithProvider_FCM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterWithProvider(testPubKey, "fcm-tok", ProviderFCM, ""); err != nil {
		t.Fatalf("RegisterWithProvider(fcm): %v", err)
	}
	got := s.All()[0]
	if got.Provider != ProviderFCM {
		t.Errorf("Provider=%q, want %q", got.Provider, ProviderFCM)
	}
	if got.ResolvedProvider() != ProviderFCM {
		t.Errorf("ResolvedProvider=%q, want %q", got.ResolvedProvider(), ProviderFCM)
	}
	if got.Environment != "" {
		t.Errorf("Environment=%q, want \"\"", got.Environment)
	}
}

// TestRegisterWithProvider_RejectsBadProvider — only "apns" and
// "fcm" are recognized. Empty defaults to "apns" via the existing
// legacy Register code path; any other string is rejected so a
// misconfigured client can't silently end up with an unrouted
// record.
func TestRegisterWithProvider_RejectsBadProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterWithProvider(testPubKey, "tok", "huawei", ""); err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if err := s.RegisterWithProvider(testPubKey, "tok", ProviderFCM, "production"); err == nil {
		t.Fatal("expected error when FCM record carries non-empty env")
	}
	if err := s.RegisterWithProvider(testPubKey, "tok", ProviderAPNs, ""); err == nil {
		t.Fatal("expected error when APNs record carries empty env")
	}
}

// TestOpenDeviceStore_MigratesLegacyJSON — a devices.json written
// before the Provider field was added must round-trip through
// OpenDeviceStore + ResolvedProvider as if it were an APNs record.
// Matches the spec scenario "Migration of existing records".
func TestOpenDeviceStore_MigratesLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.json")
	// Hand-crafted legacy payload: no provider field at all.
	legacy := `[
	{"public_key_hash":"abc123","token":"legacy-token","environment":"production","updated_at":"2025-01-01T00:00:00Z"}
]`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	regs := s.All()
	if len(regs) != 1 {
		t.Fatalf("want 1 reg, got %d", len(regs))
	}
	if regs[0].Provider != "" {
		t.Errorf("legacy entry got Provider=%q, want \"\"", regs[0].Provider)
	}
	if regs[0].ResolvedProvider() != ProviderAPNs {
		t.Errorf("legacy entry resolved to %q, want %q", regs[0].ResolvedProvider(), ProviderAPNs)
	}
	if regs[0].Token != "legacy-token" {
		t.Errorf("legacy entry token=%q, want \"legacy-token\"", regs[0].Token)
	}
	// Persisted shape should still be Provider=omitempty until the
	// next Register touches the record.
	raw, _ := os.ReadFile(path)
	if jsonContainsField(raw, "provider") {
		t.Error("legacy record was rewritten with provider field on first read; expected lazy migration")
	}
	_ = json.Valid // silence unused import in some builds
}

// jsonContainsField is a cheap substring check — we don't want to
// rewrite the test file in lockstep with json field renaming, and
// the real check (parsed JSON shape) is already covered by the
// other tests in this file.
func jsonContainsField(raw []byte, field string) bool {
	return jsonContains(raw, `"`+field+`":`)
}

func jsonContains(raw []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(raw); i++ {
		if string(raw[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
