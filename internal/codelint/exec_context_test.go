package codelint

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExecCommandTakesContext enforces the CLAUDE.md rule that every
// subprocess takes a context, so a hung shell-out can be cancelled or
// times out. A bare exec.Command is only allowed where a context would
// be wrong — a foreground interactive process (attach, $EDITOR, ssh)
// or one meant to outlive its caller (the detached daemon, the sleep
// lock holder) — and must say so with a `nocontext:` comment on the
// same line or the line above.
func TestExecCommandTakesContext(t *testing.T) {
	root := filepath.Join("..", "..")
	var violations []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		prev, n := "", 0
		for sc.Scan() {
			n++
			line := sc.Text()
			if strings.Contains(line, "exec.Command(") &&
				!strings.HasPrefix(strings.TrimSpace(line), "//") &&
				!strings.Contains(line, "nocontext:") && !strings.Contains(prev, "nocontext:") {
				rel, _ := filepath.Rel(root, path)
				violations = append(violations, fmt.Sprintf("%s:%d: %s", rel, n, strings.TrimSpace(line)))
			}
			prev = line
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Errorf("exec.Command without a context (use exec.CommandContext, or justify with a `nocontext:` comment): %s", v)
	}
}
