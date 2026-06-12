package claudemodels

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeClaudeScript writes a small shell script that mimics
// `claude -p --output-format json` by reading stdin (the prompt is
// ignored) and printing a hand-crafted JSON envelope to stdout.
// Lets us cover the full parse without invoking a real LLM.
//
// payload is the JSON to emit; exit is the script's exit code.
func fakeClaudeScript(t *testing.T, payload string, exit int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("script-based fake binary not supported on Windows in this test")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	// `cat >/dev/null` drains the piped prompt so the script doesn't
	// SIGPIPE the parent when Stdin closes. Inline the payload via a
	// heredoc to keep the script self-contained.
	body := "#!/bin/sh\ncat >/dev/null\n" +
		"cat <<'EOF'\n" + payload + "\nEOF\n" +
		"exit " + map[bool]string{true: "0", false: "1"}[exit == 0] + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}

// TestClaudeCLIFetcher_HappyPath_ParsesStructuredOutput — the success
// case the daemon hits 99% of the time when a user has `claude`
// installed and logged in.
func TestClaudeCLIFetcher_HappyPath_ParsesStructuredOutput(t *testing.T) {
	const payload = `{"type":"result","subtype":"success","is_error":false,` +
		`"structured_output":{"models":["claude-opus-4-8","claude-sonnet-4-6","claude-haiku-4-5"]}}`
	binary := fakeClaudeScript(t, payload, 0)

	got, err := ClaudeCLIFetcher{Binary: binary}.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 models, got %d: %+v", len(got), got)
	}
	wantIDs := []string{"claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5"}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Errorf("models[%d].ID = %q, want %q", i, got[i].ID, want)
		}
		if got[i].Source != SourceClaudeCLI {
			t.Errorf("models[%d].Source = %q, want %q", i, got[i].Source, SourceClaudeCLI)
		}
	}
	// Family inference still works on CLI-discovered IDs.
	if got[0].Family != "opus" || got[1].Family != "sonnet" || got[2].Family != "haiku" {
		t.Errorf("family inference broke: %+v", got)
	}
}

// TestClaudeCLIFetcher_NotInstalled_ReturnsSentinel — the
// fall-through case for users who don't have `claude` on PATH (rare
// but possible: someone installed ccmux first and is still setting
// up). Must return ErrClaudeCLIUnavailable so the Service moves on
// without a scary log line.
func TestClaudeCLIFetcher_NotInstalled_ReturnsSentinel(t *testing.T) {
	_, err := ClaudeCLIFetcher{Binary: "/nonexistent/claude-binary"}.Fetch(context.Background())
	if !errors.Is(err, ErrClaudeCLIUnavailable) {
		t.Errorf("not-installed should sentinel-error, got %v", err)
	}
}

// TestClaudeCLIFetcher_NotLoggedIn_ReturnsSentinel — common case for
// users who installed `claude` but skipped `claude auth login`. The
// CLI exits 0 but `is_error: true` with a "Not logged in" message;
// we treat that as "claude unavailable" so the chain falls through
// to the API tier instead of erroring loudly.
func TestClaudeCLIFetcher_NotLoggedIn_ReturnsSentinel(t *testing.T) {
	const payload = `{"type":"result","is_error":true,"result":"Not logged in · Please run /login"}`
	binary := fakeClaudeScript(t, payload, 0)

	_, err := ClaudeCLIFetcher{Binary: binary}.Fetch(context.Background())
	if !errors.Is(err, ErrClaudeCLIUnavailable) {
		t.Errorf("not-logged-in should sentinel-error, got %v", err)
	}
}

// TestClaudeCLIFetcher_RealError_PropagatesGenerically — non-auth
// failures (API outage, rate limit, schema mismatch) shouldn't pose
// as ErrClaudeCLIUnavailable. The Service distinguishes these so the
// caller can log them.
func TestClaudeCLIFetcher_RealError_PropagatesGenerically(t *testing.T) {
	const payload = `{"type":"result","is_error":true,"result":"upstream 503 service unavailable"}`
	binary := fakeClaudeScript(t, payload, 0)

	_, err := ClaudeCLIFetcher{Binary: binary}.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected an error on is_error: true")
	}
	if errors.Is(err, ErrClaudeCLIUnavailable) {
		t.Errorf("non-auth error should NOT be the unavailable sentinel: %v", err)
	}
}

// TestClaudeCLIFetcher_EmptyModels_IsAnError — the contract says we
// return at least one model on success. An empty `models` array is a
// schema-honoring but useless response; better to error than to
// silently make the picker empty.
func TestClaudeCLIFetcher_EmptyModels_IsAnError(t *testing.T) {
	const payload = `{"type":"result","is_error":false,"structured_output":{"models":[]}}`
	binary := fakeClaudeScript(t, payload, 0)

	_, err := ClaudeCLIFetcher{Binary: binary}.Fetch(context.Background())
	if err == nil {
		t.Error("empty models array should be an error")
	}
}

// TestService_CLIFirst_SkipsAPIWhenCLISucceeds — the contract of the
// discovery chain: CLI is the primary, the API tier doesn't get
// touched when CLI returns. Critical because the API call costs the
// user real money and is wasted work when CLI already answered.
func TestService_CLIFirst_SkipsAPIWhenCLISucceeds(t *testing.T) {
	const cliPayload = `{"type":"result","is_error":false,` +
		`"structured_output":{"models":["claude-opus-4-8"]}}`
	cliBinary := fakeClaudeScript(t, cliPayload, 0)

	// Stand up a fake API and assert it's NEVER hit.
	apiHits := 0
	apiSrv := newAPIServer(t, func() ([]modelRow, error) {
		apiHits++
		return []modelRow{{ID: "claude-from-api"}}, nil
	})
	defer apiSrv.Close()

	dir := t.TempDir()
	s := New(filepath.Join(dir, "models.json"), "k")
	s.Fetcher.BaseURL = apiSrv.URL
	s.CLIFetcher = ClaudeCLIFetcher{Binary: cliBinary}

	cat, err := s.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if cat.Source != SourceClaudeCLI {
		t.Errorf("source = %q, want %q", cat.Source, SourceClaudeCLI)
	}
	if apiHits != 0 {
		t.Errorf("CLI succeeded but API was hit %d times — chain order is wrong", apiHits)
	}
	if got := ids(cat.Models); !contains(got, "claude-opus-4-8") || contains(got, "claude-from-api") {
		t.Errorf("catalog should be CLI's models, got %v", got)
	}
}

// TestService_CLIUnavailable_FallsThroughToAPI — when claude isn't
// installed (or isn't logged in), the chain has to keep walking. API
// hits, source tagged "api". This is the daemon's API-key-user path.
func TestService_CLIUnavailable_FallsThroughToAPI(t *testing.T) {
	apiHits := 0
	apiSrv := newAPIServer(t, func() ([]modelRow, error) {
		apiHits++
		return []modelRow{{ID: "claude-from-api"}}, nil
	})
	defer apiSrv.Close()

	dir := t.TempDir()
	s := New(filepath.Join(dir, "models.json"), "k")
	s.Fetcher.BaseURL = apiSrv.URL
	// Force CLI unavailable: bogus binary + nil Run so LookPath
	// fails and we get ErrClaudeCLIUnavailable.
	s.CLIFetcher = ClaudeCLIFetcher{Binary: "/nonexistent/claude"}

	cat, err := s.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if cat.Source != SourceAPI {
		t.Errorf("source = %q, want %q", cat.Source, SourceAPI)
	}
	if apiHits != 1 {
		t.Errorf("API should have been hit exactly once, got %d", apiHits)
	}
}

// newAPIServer factors the boilerplate of standing up a stubbed
// Anthropic Models API endpoint so the chain-ordering tests above
// stay focused on the assertion, not the JSON envelope.
func newAPIServer(t *testing.T, page func() ([]modelRow, error)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := page()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		stubResponse(t, w, rows, false, "")
	}))
}

// TestClaudeCLIFetcher_ContextCancel — long-running `claude -p` calls
// (~15s) need to honor the daemon's context cancellation so a
// shutdown doesn't hang. We don't drive a real cancellation here
// (would race the fake's startup), but we do prove the Run hook
// passes the ctx through — that's the seam that makes cancellation
// possible.
func TestClaudeCLIFetcher_ContextCancel(t *testing.T) {
	called := false
	var seenCtx context.Context
	f := ClaudeCLIFetcher{
		Binary: "/nonexistent",
		Run: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			called = true
			seenCtx = ctx
			// Return a command that succeeds instantly; payload
			// content doesn't matter for this assertion.
			return exec.CommandContext(ctx, "true")
		},
	}
	ctx := context.Background()
	_, _ = f.Fetch(ctx)
	if !called {
		t.Error("Run hook never invoked — Fetch should call into it")
	}
	if seenCtx != ctx {
		t.Error("Run did not receive the parent context — ctx cancellation won't propagate")
	}
}
