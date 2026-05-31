package setupwizard

import (
	"context"
	"io"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

func TestAssumeYes(t *testing.T) {
	if assumeYes(context.Background()) {
		t.Error("assumeYes should be false without withAssumeYes")
	}
	if !assumeYes(withAssumeYes(context.Background())) {
		t.Error("assumeYes should be true after withAssumeYes")
	}
}

// TestConfirm_AssumeYes verifies --yes mode never touches Huh (no TTY
// needed) and returns the per-site autoAnswer: true for steps that are
// safe to run unattended, false for interactive-only ones (Moshi
// pairing) so they're skipped rather than auto-run.
func TestConfirm_AssumeYes(t *testing.T) {
	ctx := withAssumeYes(context.Background())
	if got, err := confirm(ctx, true, "Install X?", "desc", "Install", "Skip"); err != nil || !got {
		t.Fatalf("confirm(yes, auto=true) = (%v, %v), want (true, nil)", got, err)
	}
	if got, err := confirm(ctx, false, "Set up Moshi?", "desc", "Set up", "Later"); err != nil || got {
		t.Fatalf("confirm(yes, auto=false) = (%v, %v), want (false, nil)", got, err)
	}
}

// TestMarkSetupCompleted: a finished wizard records Setup.Completed so
// the launch nudge stops firing.
func TestMarkSetupCompleted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if cfg, _ := config.Load(); cfg.Setup.Completed {
		t.Fatal("fresh config should be incomplete")
	}
	markSetupCompleted(io.Discard)
	cfg, _ := config.Load()
	if !cfg.Setup.Completed {
		t.Error("markSetupCompleted should set Setup.Completed=true")
	}
}

// TestStepConfig_AssumeYes is the regression for the worst --yes failure
// mode: the 6-field config form MUST be skipped non-interactively,
// otherwise the step blocks forever waiting for keypresses (with a TTY)
// or errors (without one), and the wizard never finishes. In --yes mode
// it must instead persist the pre-seeded defaults and return.
func TestStepConfig_AssumeYes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := stepConfig(withAssumeYes(context.Background()), io.Discard); err != nil {
		t.Fatalf("stepConfig(--yes) returned error: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Projects.Root == "" {
		t.Error("stepConfig(--yes) should write a default projects root without prompting")
	}
}
