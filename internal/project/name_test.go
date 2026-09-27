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

// TestValidateName_RejectsControlCharacters — only NUL was refused, so
// a project named with ESC, BEL, DEL or a C1 control (POST /v1/projects,
// `ccmux new`) later printed raw escape sequences in CLI output.
func TestValidateName_RejectsControlCharacters(t *testing.T) {
	for _, bad := range []string{
		"esc\x1b[31mred", "bell\a", "ctl\x01", "del\x7f", "tab\tname", "nl\nname", "cr\rname",
		"csi\u009b31m", "c1\u0085", "raw\x9b31m", "bad\xffutf8",
	} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error", bad)
		}
	}
	for _, ok := range []string{"café", "日本語", "emoji-🚀", "with#hash", "sp ace"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", ok, err)
		}
	}
}
