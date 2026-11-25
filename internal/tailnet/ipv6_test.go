package tailnet

import "testing"

func TestPreferredTailscaleIP_PrefersIPv4(t *testing.T) {
	got := preferredTailscaleIP([]string{"fd7a:115c:a1e0::1", "100.64.0.5"})
	if got != "100.64.0.5" {
		t.Errorf("preferredTailscaleIP = %q, want 100.64.0.5", got)
	}
	if got := preferredTailscaleIP([]string{"fd7a:115c:a1e0::1"}); got != "fd7a:115c:a1e0::1" {
		t.Errorf("IPv6-only preferredTailscaleIP = %q, want fd7a:115c:a1e0::1", got)
	}
}
