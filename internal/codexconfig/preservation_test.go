package codexconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteSettings_PreservesStructuredApprovalPolicyAndLocalDate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	cfgPath := filepath.Join(home, "config.toml")
	orig := `model = "o3"
approval_policy = { reject = { rules = ["rm -rf"] } }
review_date = 2026-05-01
`
	if err := os.WriteFile(cfgPath, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEffortLevel("high"); err != nil {
		t.Fatalf("SetEffortLevel: %v", err)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	if !strings.Contains(text, "approval_policy") || !strings.Contains(text, "rm -rf") {
		t.Errorf("structured approval_policy was lost:\n%s", text)
	}
	if !strings.Contains(text, "review_date = 2026-05-01") {
		t.Errorf("local date was corrupted:\n%s", text)
	}
	if !strings.Contains(text, `model_reasoning_effort = "high"`) {
		t.Errorf("effort level not written:\n%s", text)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("config.toml mode = %04o, want 0600", fi.Mode().Perm())
		}
	}
}
