package openrouterusage

import (
	"math"
	"testing"
)

func TestParsePrice_RejectsNaNAndInf(t *testing.T) {
	for _, bad := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf", "-1.5"} {
		got := parsePrice(bad)
		if got != 0 || math.IsNaN(got) || math.IsInf(got, 0) {
			t.Errorf("parsePrice(%q) = %v, want 0", bad, got)
		}
	}
}
