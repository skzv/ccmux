package config

import "testing"

// TestSetupConfig_RoundTrip pins the [setup] section that drives the
// first-run nudge: defaults are zero, and Completed/NudgeDismissed
// survive Save/Load.
func TestSetupConfig_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if d := Defaults(); d.Setup.Completed || d.Setup.NudgeDismissed {
		t.Fatalf("defaults should be zero-valued: %+v", d.Setup)
	}

	in := Defaults()
	in.Setup.Completed = true
	in.Setup.NudgeDismissed = true
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Setup.Completed || !got.Setup.NudgeDismissed {
		t.Errorf("round-trip lost setup flags: %+v", got.Setup)
	}
}
