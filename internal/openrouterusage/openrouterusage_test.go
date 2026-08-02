package openrouterusage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeOR stands up an httptest server speaking OpenRouter's documented
// JSON shapes so the client is tested end to end without a live key.
func fakeOR(t *testing.T, keyJSON, modelsJSON string, wantAuth string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); wantAuth != "" && got != wantAuth {
			t.Errorf("Authorization = %q, want %q", got, wantAuth)
		}
		switch r.URL.Path {
		case "/key":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(keyJSON))
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(modelsJSON))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return New("sk-test-key", srv.URL)
}

// TestKey_ParsesUsageAndLimit — the spend headline. usage + limit come
// back as the documented {"data":{...}} envelope; Remaining is computed.
func TestKey_ParsesUsageAndLimit(t *testing.T) {
	body := `{"data":{"label":"ccmux key","usage":12.5,"limit":50,"is_free_tier":false,
	          "rate_limit":{"requests":200,"interval":"10s"}}}`
	c := fakeOR(t, body, "", "Bearer sk-test-key")
	k, err := c.Key(context.Background())
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if k.Usage != 12.5 {
		t.Errorf("Usage = %v, want 12.5", k.Usage)
	}
	if k.Limit == nil || *k.Limit != 50 {
		t.Errorf("Limit = %v, want 50", k.Limit)
	}
	if k.Remaining() != 37.5 {
		t.Errorf("Remaining = %v, want 37.5", k.Remaining())
	}
	if k.IsFreeTier {
		t.Error("IsFreeTier = true, want false")
	}
	if k.RateLimit == nil || k.RateLimit.Requests != 200 {
		t.Errorf("RateLimit not parsed: %+v", k.RateLimit)
	}
}

// TestKey_UnlimitedKey — a null limit means pay-as-you-go; Remaining
// reports -1 (the sentinel for "uncapped") so the UI shows spend without
// a misleading "remaining" figure.
func TestKey_UnlimitedKey(t *testing.T) {
	c := fakeOR(t, `{"data":{"usage":3.0,"limit":null}}`, "", "")
	k, err := c.Key(context.Background())
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if k.Limit != nil {
		t.Errorf("Limit = %v, want nil (uncapped)", k.Limit)
	}
	if k.Remaining() != -1 {
		t.Errorf("Remaining = %v, want -1 for uncapped key", k.Remaining())
	}
}

// TestKey_RemainingClampsAtZero — a key over its limit must not report
// negative headroom.
func TestKey_RemainingClampsAtZero(t *testing.T) {
	lim := 10.0
	k := KeyInfo{Usage: 12.0, Limit: &lim}
	if k.Remaining() != 0 {
		t.Errorf("Remaining = %v, want 0 (clamped)", k.Remaining())
	}
}

// TestKey_Unauthorized — a 401 surfaces a clear "check api_key" error,
// not a generic decode failure, so the dashboard can tell the user
// exactly what's wrong.
func TestKey_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := New("bad", srv.URL)
	_, err := c.Key(context.Background())
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !contains(err.Error(), "unauthorized") {
		t.Errorf("error = %q, want it to mention unauthorized", err.Error())
	}
}

// TestPricing_ParsesPerTokenStrings — the per-model price table. Decimal
// strings parse to floats; CostOf does the arithmetic.
func TestPricing_ParsesPerTokenStrings(t *testing.T) {
	models := `{"data":[
	  {"id":"anthropic/claude-opus","pricing":{"prompt":"0.000015","completion":"0.000075"}},
	  {"id":"openai/gpt-5","pricing":{"prompt":"0.0000025","completion":"0.00001"}}
	]}`
	c := fakeOR(t, "", models, "")
	prices, err := c.Pricing(context.Background())
	if err != nil {
		t.Fatalf("Pricing: %v", err)
	}
	opus, ok := prices["anthropic/claude-opus"]
	if !ok {
		t.Fatal("opus pricing missing")
	}
	if opus.PromptUSD != 0.000015 || opus.OutputUSD != 0.000075 {
		t.Errorf("opus price = %+v", opus)
	}
	// 1000 input + 500 output tokens at opus rates.
	want := 1000*0.000015 + 500*0.000075
	if got := opus.CostOf(1000, 500); got != want {
		t.Errorf("CostOf = %v, want %v", got, want)
	}
}

// TestPricing_MalformedPriceIsFreeNotFatal — one model with a junk price
// string must not blank the whole table; it parses as 0 (free).
func TestPricing_MalformedPriceIsFreeNotFatal(t *testing.T) {
	models := `{"data":[
	  {"id":"good","pricing":{"prompt":"0.001","completion":"0.002"}},
	  {"id":"weird","pricing":{"prompt":"not-a-number","completion":""}}
	]}`
	c := fakeOR(t, "", models, "")
	prices, err := c.Pricing(context.Background())
	if err != nil {
		t.Fatalf("Pricing: %v", err)
	}
	if len(prices) != 2 {
		t.Fatalf("got %d models, want 2 (a bad price must not drop the row)", len(prices))
	}
	if prices["weird"].PromptUSD != 0 || prices["weird"].OutputUSD != 0 {
		t.Errorf("malformed price should parse as 0, got %+v", prices["weird"])
	}
	if prices["good"].PromptUSD != 0.001 {
		t.Errorf("good model price corrupted: %+v", prices["good"])
	}
}

// TestDefaultBaseURL — an empty baseURL falls back to the real
// OpenRouter root so production config that omits base_url still works.
func TestDefaultBaseURL(t *testing.T) {
	c := New("k", "")
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
	// Trailing slash is trimmed so path joins don't double up.
	c2 := New("k", "https://example.com/api/v1/")
	if c2.baseURL != "https://example.com/api/v1" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", c2.baseURL)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
