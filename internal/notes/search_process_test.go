//go:build !windows

package notes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSearchRipgrep_ReapsProcess(t *testing.T) {
	for _, mode := range []string{"limit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			pidFile := filepath.Join(root, "pid")
			t.Setenv("PATH", root)
			t.Setenv("CCMUX_TEST_RG_PID", pidFile)
			script := "#!/bin/sh\necho $$ > \"$CCMUX_TEST_RG_PID\"\n"
			if mode == "limit" {
				script += `printf '%s\n' '{"type":"match","data":{"path":{"text":"note.md"},"lines":{"text":"match"},"line_number":1}}'` + "\n"
			}
			// exec preserves the PID and avoids leaving a shell child behind.
			script += "exec /bin/sleep 30\n"
			if err := os.WriteFile(filepath.Join(root, "rg"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type result struct {
				hits []SearchHit
				err  error
			}
			done := make(chan result, 1)
			go func() {
				hits, err := (Vault{Root: root}).searchRipgrep(ctx, "match", 1)
				done <- result{hits, err}
			}()
			var pid int
			for pid == 0 {
				b, _ := os.ReadFile(pidFile)
				pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
				if ctx.Err() != nil {
					t.Fatal("fake ripgrep did not start")
				}
				time.Sleep(time.Millisecond)
			}
			if mode == "cancel" {
				cancel()
			}
			r := <-done
			if mode == "limit" && (r.err != nil || len(r.hits) != 1) {
				t.Fatalf("limit result: hits=%d err=%v", len(r.hits), r.err)
			}
			if mode == "cancel" && !errors.Is(r.err, context.Canceled) {
				t.Fatalf("cancel result: %v", r.err)
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("rg process %d still exists after search returned: %v", pid, err)
			}
		})
	}
}
