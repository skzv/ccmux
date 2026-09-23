package project

import "testing"

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"foo", "my-app", "Proj 2", "a.b"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "../x", "..", ".", "a/b", `a\b`, ".hidden", "x\x00y"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error", bad)
		}
	}
}
