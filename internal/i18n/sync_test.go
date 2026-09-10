package i18n

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
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
// the TUI must have an entry in every non-English catalog. Its failure output doubles as the
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
	format := regexp.MustCompile(`%(?:\[(\d+)\])?([-+# 0]*(?:\d+|\*)?(?:\.(?:\d+|\*))?)([a-zA-Z%])`)
	signature := func(value string) []string {
		next := 1
		var result []string
		for _, match := range format.FindAllStringSubmatch(value, -1) {
			if match[3] == "%" {
				result = append(result, "literal-percent")
				continue
			}
			if match[1] != "" {
				next, _ = strconv.Atoi(match[1])
			}
			result = append(result, fmt.Sprintf("%d:%s%s", next, match[2], match[3]))
			next++
		}
		sort.Strings(result)
		return result
	}
	for _, language := range Languages() {
		if language.Code == LangEn {
			continue
		}
		t.Run(string(language.Code), func(t *testing.T) {
			table := map[string]string{}
			b, err := localesFS.ReadFile("locales/" + string(language.Code) + ".toml")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tomlDecode(string(b), &table); err != nil {
				t.Fatal(err)
			}
			for _, key := range keys {
				value, ok := table[key]
				if !ok || strings.TrimSpace(value) == "" {
					t.Errorf("missing translation: %q", key)
				}
			}
			for key, value := range table {
				if !strings.Contains(key, "\n") && strings.Contains(value, "\n") {
					t.Errorf("unexpected line break in %q", key)
				}
				if strings.Contains(value, "ZXQ") || strings.Contains(value, "[CCMUX") {
					t.Errorf("translation marker in %q", key)
				}
				if !reflect.DeepEqual(signature(key), signature(value)) {
					t.Errorf("format placeholders differ: %q => %q", key, value)
				}
			}
		})
	}
}
