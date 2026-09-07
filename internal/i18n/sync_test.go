package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// collectTRKeys walks internal/tui (recursively, skipping testdata and
// _test.go files) and returns every literal first argument of a tr("…")
// call, plus agent-browser section titles translated at render time.
func collectTRKeys(t *testing.T) []string {
	t.Helper()
	var keys []string
	fset := token.NewFileSet()
	err := filepath.WalkDir("../tui", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if perr != nil {
			t.Errorf("parse %s: %v", path, perr)
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if section, ok := n.(*ast.CompositeLit); ok {
				if typ, ok := section.Type.(*ast.Ident); ok && typ.Name == "agentBrowserSection" {
					for _, elt := range section.Elts {
						field, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						if name, ok := field.Key.(*ast.Ident); ok && name.Name == "Title" {
							if lit, ok := field.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
								if key, err := strconv.Unquote(lit.Value); err == nil {
									keys = append(keys, key)
								}
							}
						}
					}
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "tr" || len(call.Args) < 1 {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil {
					keys = append(keys, v)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/tui: %v", err)
	}
	sort.Strings(keys)
	return keys
}

// TestTUILKeysSynced is the i18n invariant: every tr() key referenced by
// the TUI must have a zh.toml entry. Its failure output doubles as the
// machine-readable "which keys still need translating" checklist.
func TestTUILKeysSynced(t *testing.T) {
	keys := collectTRKeys(t)
	if len(keys) == 0 {
		// No tr() calls in the TUI yet (the first one lands with the
		// Settings language row). Skip rather than fail so an early
		// commit isn't red; the moment any tr() exists this test starts
		// enforcing the invariant.
		t.Skip("no tr() calls in the TUI yet — nothing to sync")
	}
	zh := map[string]string{}
	b, err := localesFS.ReadFile("locales/zh.toml")
	if err != nil {
		t.Fatalf("read zh.toml: %v", err)
	}
	if _, err := tomlDecode(string(b), &zh); err != nil {
		t.Fatalf("decode zh.toml: %v", err)
	}
	missing := []string{}
	for _, k := range keys {
		if _, ok := zh[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Errorf("tr() keys missing from locales/zh.toml (%d):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}
