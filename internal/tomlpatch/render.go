package tomlpatch

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// ranks maps each key path to the position it first appears at across
// docs, so keys the patch adds come out in the order the full
// re-encode would have written them.
func ranks(docs ...toml.MetaData) map[string]int {
	r := map[string]int{}
	n := 0
	for _, md := range docs {
		for _, k := range md.Keys() {
			s := strings.Join(k, "\x00")
			if _, ok := r[s]; !ok {
				r[s] = n
			}
			n++
		}
	}
	return r
}

// orderedKeys returns m's keys in target-document order (unknown keys
// last, alphabetically).
func (p *patcher) orderedKeys(path []string, m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		if r, ok := p.rank[strings.Join(append(clone(path), k), "\x00")]; ok {
			return r
		}
		return math.MaxInt
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := rank(keys[i]), rank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// renderKey writes k bare when TOML allows it, quoted otherwise.
func renderKey(k string) (string, error) {
	if k != "" && strings.IndexFunc(k, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) < 0 {
		return k, nil
	}
	return renderScalar(k)
}

func renderPath(path []string) (string, error) {
	parts := make([]string, len(path))
	for i, k := range path {
		s, err := renderKey(k)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return strings.Join(parts, "."), nil
}

// renderScalar formats one non-table value exactly as the full
// re-encode (BurntSushi's encoder) would.
func renderScalar(v any) (string, error) {
	if t, ok := v.(time.Time); ok {
		// The decoder gives local (offset-less) datetimes a zone named
		// after their kind, offset by the machine's UTC offset, and the
		// encoder converts them to UTC before formatting — so a decoded
		// 07:32:00 re-encodes as 14:32:00 in UTC-7. Format the wall clock
		// directly so the value decodes back to itself.
		switch t.Location().String() {
		case "datetime-local":
			return t.Format("2006-01-02T15:04:05.999999999"), nil
		case "date-local":
			return t.Format("2006-01-02"), nil
		case "time-local":
			return t.Format("15:04:05.999999999"), nil
		}
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(map[string]any{"v": v}); err != nil {
		return "", fmt.Errorf("tomlpatch: render value: %w", err)
	}
	s := buf.String()
	if !strings.HasPrefix(s, "v = ") || !strings.HasSuffix(s, "\n") || strings.Count(s, "\n") != 1 {
		return "", fmt.Errorf("tomlpatch: render value: unexpected encoding %q", s)
	}
	return s[len("v = ") : len(s)-1], nil
}

// renderValue formats v as a single-line TOML value: tables become
// inline tables, arrays of tables inline arrays.
func (p *patcher) renderValue(path []string, v any) (string, error) {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			return "{}", nil
		}
		parts := make([]string, 0, len(x))
		for _, k := range p.orderedKeys(path, x) {
			ks, err := renderKey(k)
			if err != nil {
				return "", err
			}
			vs, err := p.renderValue(concat(path, []string{k}), x[k])
			if err != nil {
				return "", err
			}
			parts = append(parts, ks+" = "+vs)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	case []any, []map[string]any:
		l, _ := asList(x)
		parts := make([]string, len(l))
		for i, e := range l {
			s, err := p.renderValue(path, e)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	default:
		return renderScalar(v)
	}
}

// renderLine formats `key = value` for a key path relative to the
// table the line goes in.
func (p *patcher) renderLine(indent string, rel, full []string, v any) (string, error) {
	ks, err := renderPath(rel)
	if err != nil {
		return "", err
	}
	vs, err := p.renderValue(full, v)
	if err != nil {
		return "", err
	}
	return indent + ks + " = " + vs + p.nl, nil
}

// renderSection writes a [path] (or [[path]] when array) section for
// table m: its plain keys first, then its sub-tables, the order TOML
// requires. Indentation follows the file's own style.
func (p *patcher) renderSection(sb *strings.Builder, path []string, m map[string]any, array bool) error {
	hdr, err := renderPath(path)
	if err != nil {
		return err
	}
	lb, rb := "[", "]"
	if array {
		lb, rb = "[[", "]]"
	}
	sb.WriteString(strings.Repeat(p.d.unit, len(path)-1) + lb + hdr + rb + p.nl)
	keys := p.orderedKeys(path, m)
	indent := strings.Repeat(p.d.unit, len(path))
	for _, k := range keys {
		if isSection(m[k]) {
			continue
		}
		line, err := p.renderLine(indent, []string{k}, concat(path, []string{k}), m[k])
		if err != nil {
			return err
		}
		sb.WriteString(line)
	}
	for _, k := range keys {
		sub := concat(path, []string{k})
		if cm, ok := m[k].(map[string]any); ok {
			if err := p.renderSection(sb, sub, cm, false); err != nil {
				return err
			}
			continue
		}
		if elems, ok := tableList(m[k]); ok {
			for _, e := range elems {
				if err := p.renderSection(sb, sub, e, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// isSection reports whether the encoder writes v as its own [table] or
// [[array]] section rather than as a key/value line.
func isSection(v any) bool {
	if _, ok := v.(map[string]any); ok {
		return true
	}
	_, ok := tableList(v)
	return ok
}
