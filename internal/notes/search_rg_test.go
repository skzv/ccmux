//go:build !windows

package notes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fakeRipgrep puts a shell-script `rg` first on PATH. It records its
// arguments (one per line) in the returned file, prints `stdout`, and
// exits with `code`.
func fakeRipgrep(t *testing.T, stdout string, code int) (argsFile string) {
	t.Helper()
	t.Setenv("CCMUX_TEST_RG_OUT", stdout)
	return fakeRipgrepScript(t, "printf '%s' \"$CCMUX_TEST_RG_OUT\"\n"+
		"echo 'rg: some/dir: Permission denied (os error 13)' >&2\n"+
		"exit "+string(rune('0'+code))+"\n")
}

// fakeRipgrepScript installs an `rg` that records its arguments and then
// runs body.
func fakeRipgrepScript(t *testing.T, body string) (argsFile string) {
	t.Helper()
	bin := t.TempDir()
	argsFile = filepath.Join(bin, "args")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CCMUX_TEST_RG_ARGS", argsFile)
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > \"$CCMUX_TEST_RG_ARGS\"\n" + body
	if err := os.WriteFile(filepath.Join(bin, "rg"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return argsFile
}

// TestSearchRipgrep_LongOutputLineSkipped — regression: rg's JSON record
// for a match on a >4 MiB line overflowed the bufio.Scanner and the
// whole search failed with "token too long". The record is skipped now
// and later matches still arrive.
func TestSearchRipgrep_LongOutputLineSkipped(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CCMUX_TEST_RG_FILE", filepath.Join(root, "a.md"))
	fakeRipgrepScript(t, `printf '{"type":"match","data":{"path":{"text":"%s"},"lines":{"text":"' "$CCMUX_TEST_RG_FILE"
head -c 5242880 /dev/zero | tr '\0' x
printf '"},"line_number":1}}\n'
printf '{"type":"match","data":{"path":{"text":"%s"},"lines":{"text":"short match"},"line_number":2}}\n' "$CCMUX_TEST_RG_FILE"
exit 0
`)
	hits, err := Vault{Root: root}.searchRipgrep(context.Background(), "match", 100)
	if err != nil {
		t.Fatalf("searchRipgrep: %v", err)
	}
	if len(hits) != 1 || hits[0].LineNum != 2 || hits[0].Snippet != "short match" {
		t.Fatalf("hits = %+v, want only the line-2 match", hits)
	}
}

// TestSearchRipgrep_ExitTwoKeepsHits — regression: rg exits 2 when any
// path errors (an unreadable subdirectory) even though it searched and
// matched everything else; ccmux threw the hits away and reported
// "rg: exit status 2".
func TestSearchRipgrep_ExitTwoKeepsHits(t *testing.T) {
	root := t.TempDir()
	match := `{"type":"match","data":{"path":{"text":"` + filepath.Join(root, "a.md") + `"},"lines":{"text":"found it\n"},"line_number":4}}` + "\n"
	fakeRipgrep(t, match, 2)
	hits, err := Vault{Root: root}.searchRipgrep(context.Background(), "found", 100)
	if err != nil {
		t.Fatalf("exit 2 with hits must not be an error: %v", err)
	}
	if len(hits) != 1 || hits[0].Rel != "a.md" || hits[0].LineNum != 4 {
		t.Fatalf("hits = %+v", hits)
	}
}

// TestSearchRipgrep_ExitTwoWithoutHitsFallsBack — rg failing outright
// (no output, exit 2) must not hide results the Go scanner can find.
func TestSearchRipgrep_ExitTwoWithoutHitsFallsBack(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("x\nfound it\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeRipgrep(t, "", 2)
	hits, err := Vault{Root: root}.searchRipgrep(context.Background(), "found", 100)
	if err != nil || len(hits) != 1 || hits[0].LineNum != 2 {
		t.Fatalf("hits = %+v, err = %v; want the fallback's hit", hits, err)
	}
}

// TestSearchRipgrep_FileSetMatchesList — regression: rg ran with its
// defaults (.gitignore honored, case-sensitive `--type md`), so notes
// List showed — gitignored ones, NOTE.MD — were unsearchable. The
// invocation must mirror List's walk.
func TestSearchRipgrep_FileSetMatchesList(t *testing.T) {
	argsFile := fakeRipgrep(t, "", 1)
	if _, err := (Vault{Root: t.TempDir()}).searchRipgrep(context.Background(), "q", 10); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := "\n" + string(raw)
	for _, want := range []string{
		"\n--no-ignore\n", "\n--hidden\n", "\n--iglob\n*.md\n", "\n--glob\n!.*/\n",
		"\n--glob\n!node_modules/\n", "\n--glob\n!__pycache__/\n", "\n--no-config\n",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("rg args missing %q:\n%s", strings.TrimSpace(want), raw)
		}
	}
	if strings.Contains(args, "\n--type\n") {
		t.Errorf("rg args still use --type (case-sensitive, wider than List):\n%s", raw)
	}
}

// TestSearch_SameFilesAsList runs each backend over a vault with
// ignore files, hidden entries, pruned dirs, an upper-case extension
// and an unreadable subdirectory, and checks search finds exactly the
// notes List shows. The ripgrep leg needs a real rg on PATH.
func TestSearch_SameFilesAsList(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read mode-000 directories")
	}
	for _, backend := range []string{"fallback", "ripgrep"} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{
				".gitignore":            "gitignored/\n",
				".ignore":               "dotignored/\n",
				".git/HEAD":             "ref: refs/heads/main\n",
				"a.md":                  "needle a\n",
				"NOTE.MD":               "needle upper\n",
				".hidden-file.md":       "needle hidden file\n",
				"gitignored/g.md":       "needle gitignored\n",
				"dotignored/d.md":       "needle dotignored\n",
				"docs/deep/n.md":        "needle deep\n",
				".obsidian/o.md":        "needle hidden dir\n",
				"node_modules/pkg/r.md": "needle vendored\n",
				"readme.markdown":       "needle other ext\n",
				"locked/l.md":           "needle locked\n",
			}
			for rel, body := range files {
				p := filepath.Join(root, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			locked := filepath.Join(root, "locked")
			if err := os.Chmod(locked, 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

			v := Vault{Root: root}
			search := v.searchFallback
			if backend == "ripgrep" {
				if _, err := exec.LookPath("rg"); err != nil {
					t.Skip("ripgrep unavailable")
				}
				search = v.searchRipgrep
			}
			hits, err := search(context.Background(), "needle", 100)
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			entries, err := v.List()
			if err != nil {
				t.Fatal(err)
			}
			var listed, found []string
			for _, e := range entries {
				listed = append(listed, e.Rel)
			}
			for _, h := range hits {
				found = append(found, h.Rel)
			}
			sort.Strings(listed)
			sort.Strings(found)
			want := []string{".hidden-file.md", "NOTE.MD", "a.md", "docs/deep/n.md", "dotignored/d.md", "gitignored/g.md"}
			if strings.Join(listed, ",") != strings.Join(want, ",") {
				t.Fatalf("List = %v, want %v", listed, want)
			}
			if strings.Join(found, ",") != strings.Join(listed, ",") {
				t.Errorf("search found %v, List shows %v", found, listed)
			}
		})
	}
}
