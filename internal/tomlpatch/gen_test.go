package tomlpatch

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	gotoml "github.com/pelletier/go-toml/v2"
)

// This file is the property harness behind FuzzPatch and
// TestPatch_Property: a byte-driven generator writes a random but
// valid TOML document from a small grammar (comments everywhere, blank
// lines, indentation, quoting styles, dotted keys, implicit tables,
// arrays of tables, CRLF), then randomly changes its decoded data. The
// patched text must decode to exactly the changed data, and every
// comment and untouched line must survive.

// src hands out choices from the fuzzer's bytes; exhausted input reads
// as zeros, so every input yields a document.
type src struct {
	data []byte
	i    int
}

func (s *src) byte() byte {
	if s.i >= len(s.data) {
		return 0
	}
	b := s.data[s.i]
	s.i++
	return b
}

// n returns a choice in [0, k).
func (s *src) n(k int) int { return int(s.byte()) % k }

// p is true pct% of the time.
func (s *src) p(pct int) bool { return s.n(100) < pct }

type genComment struct {
	text    string
	owners  [][]string
	inValue bool // inside a multi-line array: lost if that value changes
}

type genLine struct {
	text string
	path []string
}

type gen struct {
	s        *src
	sb       strings.Builder
	nl       string
	unit     string
	comments []genComment
	lines    []genLine
	nComment int
	// open is the run of standalone comments written since the last
	// non-comment line: a header written next adopts them.
	open []int
	cur  []string // path of the section being written
}

func (g *gen) comment() string {
	g.nComment++
	return fmt.Sprintf("# c%d", g.nComment)
}

func (g *gen) line(s string) {
	g.sb.WriteString(s)
	g.sb.WriteString(g.nl)
}

func (g *gen) blank() {
	g.line("")
	g.open = nil
}

// standalone writes a comment line owned by the section at owner.
func (g *gen) standalone(indent string, owner []string) {
	c := g.comment()
	g.line(indent + c)
	g.comments = append(g.comments, genComment{text: c, owners: [][]string{owner}})
	g.open = append(g.open, len(g.comments)-1)
}

type key struct{ text, name string }

var keyPool = []key{
	{"alpha", "alpha"}, {"beta", "beta"}, {"gamma", "gamma"}, {"delta", "delta"},
	{"with-dash", "with-dash"}, {"under_score", "under_score"}, {"42", "42"},
	{`"quoted key"`, "quoted key"}, {`'lit key'`, "lit key"}, {`"é"`, "é"},
	{`"dot.ted"`, "dot.ted"}, {"eps", "eps"}, {"zeta", "zeta"},
}

// keys picks up to n distinct keys not in used.
func (g *gen) keys(n int, used map[string]bool) []key {
	var out []key
	for tries := 0; len(out) < n && tries < 3*n+3; tries++ {
		k := keyPool[g.s.n(len(keyPool))]
		if used[k.name] {
			continue
		}
		used[k.name] = true
		out = append(out, k)
	}
	return out
}

var scalarPool = []string{
	`42`, `-7`, `+3`, `1_000`, `0x1F`, `0o17`, `0b101`, `3.14`, `-0.5e3`, `inf`, `nan`,
	`true`, `false`, `"plain"`, `"q \"uo\" te"`, `"back\\slash"`, `"uni é ✓"`, `"tab\tnl\n"`, `""`,
	`'literal \ raw'`, `''`, `1979-05-27T07:32:00Z`, `1979-05-27T00:32:00-07:00`,
	// No local (offset-less) dates or times: BurntSushi's encoder shifts
	// those by the machine's UTC offset, so off-UTC the target itself
	// would differ from the intended data. TestRenderScalar_LocalTimes
	// covers the patcher's own formatting of them.
}

// valueText writes a value; comments inside a multi-line array are
// recorded against owner.
func (g *gen) valueText(owner []string, depth int) (text string, multiline bool) {
	switch c := g.s.n(10); {
	case c < 6 || depth > 1:
		return scalarPool[g.s.n(len(scalarPool))], false
	case c == 6:
		n := g.s.n(4)
		parts := make([]string, n)
		for i := range parts {
			parts[i], _ = g.valueText(owner, depth+1)
		}
		return "[" + strings.Join(parts, ", ") + "]", false
	case c == 7:
		used := map[string]bool{}
		ks := g.keys(g.s.n(3), used)
		parts := make([]string, len(ks))
		for i, k := range ks {
			v, _ := g.valueText(owner, depth+1)
			parts[i] = k.text + " = " + v
		}
		if len(parts) == 0 {
			return "{}", false
		}
		return "{ " + strings.Join(parts, ", ") + " }", false
	case c == 8 && depth == 0:
		// Multi-line array with comments between the elements.
		var sb strings.Builder
		sb.WriteString("[" + g.nl)
		for i, n := 0, 1+g.s.n(3); i < n; i++ {
			v := scalarPool[g.s.n(13)] // numbers/bools/plain strings
			sb.WriteString("  " + v + ",")
			if g.s.p(50) {
				c := g.comment()
				sb.WriteString(" " + c)
				g.comments = append(g.comments, genComment{text: c, owners: [][]string{owner}, inValue: true})
			}
			sb.WriteString(g.nl)
		}
		sb.WriteString("]")
		return sb.String(), true
	default:
		return `"""multi` + g.nl + `line"""`, true
	}
}

// kvs writes n key/value lines of the table at path, some of them
// grouped under a dotted-key prefix.
func (g *gen) kvs(indent string, path []string, used map[string]bool, n int) {
	var dotted string
	if g.s.p(25) {
		for _, name := range []string{"grp", "grp2", "grp3"} {
			if !used[name] {
				dotted = name
				used[name] = true
				break
			}
		}
	}
	dUsed := map[string]bool{}
	for i := 0; i < n; i++ {
		if g.s.p(15) {
			g.standalone(indent, path)
		}
		if g.s.p(10) {
			g.blank()
		}
		k := g.keys(1, used)
		var kpath []string
		var ktext string
		if dotted != "" && g.s.p(50) {
			dk := g.keys(1, dUsed)
			if len(dk) == 0 {
				continue
			}
			sep := "."
			if g.s.p(20) {
				sep = " . "
			}
			ktext = dotted + sep + dk[0].text
			kpath = concat(path, []string{dotted, dk[0].name})
		} else {
			if len(k) == 0 {
				continue
			}
			ktext, kpath = k[0].text, concat(path, []string{k[0].name})
		}
		eq := " = "
		if g.s.p(15) {
			eq = "="
		}
		v, multi := g.valueText(kpath, 0)
		text := indent + ktext + eq + v
		if g.s.p(30) {
			c := g.comment()
			text += "   " + c
			g.comments = append(g.comments, genComment{text: c, owners: [][]string{kpath, path}})
		}
		g.line(text)
		g.open = nil
		if !multi {
			g.lines = append(g.lines, genLine{text: text, path: kpath})
		}
	}
	if len(dUsed) == 0 && dotted != "" {
		delete(used, dotted)
	}
}

// header writes a [path] / [[path]] line; comments directly above it
// now document it as well.
func (g *gen) header(text string, path []string) {
	if g.s.p(40) {
		g.blank()
	}
	if g.s.p(30) {
		g.standalone("", path)
	}
	for _, i := range g.open {
		g.comments[i].owners = append(g.comments[i].owners, path)
	}
	g.open = nil
	indent := ""
	if len(path) > 1 && g.unit != "" {
		indent = g.unit
	}
	if g.s.p(10) && !strings.HasPrefix(text, "[[") {
		text = "[ " + text[1:len(text)-1] + " ]"
	}
	if g.s.p(25) {
		c := g.comment()
		text += " " + c
		g.comments = append(g.comments, genComment{text: c, owners: [][]string{path}})
	}
	g.line(indent + text)
	g.cur = path
}

func (g *gen) body(path []string, used map[string]bool) {
	g.kvs(g.unit, path, used, g.s.n(4))
	if g.s.p(20) {
		g.standalone(g.unit, path)
	}
}

var tablePool = []key{
	{"server", "server"}, {"tools", "tools"}, {`"quoted.table"`, "quoted.table"},
	{"db", "db"}, {"ui", "ui"}, {"extra", "extra"},
}

func (g *gen) document() string {
	g.nl = "\n"
	if g.s.p(10) {
		g.nl = "\r\n"
	}
	g.unit = []string{"", "", "  ", "\t"}[g.s.n(4)]
	if g.s.p(40) {
		g.standalone("", nil)
		if g.s.p(50) {
			g.blank()
		}
	}
	rootUsed := map[string]bool{}
	g.kvs("", nil, rootUsed, g.s.n(4))
	usedTables := map[string]bool{}
	for i, n := 0, g.s.n(5); i < n; i++ {
		switch g.s.n(4) {
		case 0, 1: // [t] with an optional [t.sub]
			tk := g.pick(tablePool, usedTables, rootUsed)
			if tk == nil {
				continue
			}
			path := []string{tk.name}
			g.header("["+tk.text+"]", path)
			used := map[string]bool{}
			g.body(path, used)
			if g.s.p(40) {
				sub := g.keys(1, used)
				if len(sub) == 1 {
					sp := concat(path, []string{sub[0].name})
					g.header("["+tk.text+"."+sub[0].text+"]", sp)
					g.body(sp, map[string]bool{})
				}
			}
		case 2: // implicit: [imp.a] (and maybe [imp.b]) with no [imp]
			tk := g.pick([]key{{"imp", "imp"}, {"imp2", "imp2"}}, usedTables, rootUsed)
			if tk == nil {
				continue
			}
			used := map[string]bool{}
			for _, sub := range g.keys(1+g.s.n(2), used) {
				sp := []string{tk.name, sub.name}
				g.header("["+tk.text+"."+sub.text+"]", sp)
				g.body(sp, map[string]bool{})
			}
		default: // [[arr]] elements, each maybe with an [arr.sub]
			tk := g.pick([]key{{"arr", "arr"}, {"servers", "servers"}, {`"q arr"`, "q arr"}}, usedTables, rootUsed)
			if tk == nil {
				continue
			}
			path := []string{tk.name}
			for e, n := 0, 1+g.s.n(3); e < n; e++ {
				g.header("[["+tk.text+"]]", path)
				used := map[string]bool{}
				g.body(path, used)
				if g.s.p(25) {
					sub := g.keys(1, used)
					if len(sub) == 1 {
						sp := concat(path, []string{sub[0].name})
						g.header("["+tk.text+"."+sub[0].text+"]", sp)
						g.body(sp, map[string]bool{})
					}
				}
			}
		}
	}
	if g.s.p(20) {
		g.blank()
		g.standalone("", g.cur)
	}
	out := g.sb.String()
	if g.s.p(10) {
		out = strings.TrimSuffix(out, g.nl)
	}
	return out
}

func (g *gen) pick(pool []key, used, rootUsed map[string]bool) *key {
	k := pool[g.s.n(len(pool))]
	if used[k.name] || rootUsed[k.name] {
		return nil
	}
	used[k.name] = true
	return &k
}

// mutation records what a change did, so the checks know which
// comments and lines may legitimately be gone.
type mutation struct {
	s       *src
	removed [][]string // deleted, or changed between table and non-table
	set     [][]string // value replaced in place, or key added
	arrays  [][]string // arrays of tables that changed (alignment decides which element moves)
	fresh   int
}

func (mu *mutation) freshKey() string {
	mu.fresh++
	if mu.s.p(20) {
		return fmt.Sprintf("new key %d", mu.fresh)
	}
	return fmt.Sprintf("new%d", mu.fresh)
}

func (mu *mutation) value(depth int) any {
	switch c := mu.s.n(12); {
	case c < 3:
		return int64(mu.s.n(2000) - 1000)
	case c == 3:
		return []float64{1.5, -0.25, 3, 1e10, math.Inf(1), math.NaN()}[mu.s.n(6)]
	case c == 4:
		return mu.s.p(50)
	case c < 8:
		return []string{"new", "with \"quote\"", "é ✓", "", "back\\slash", "line\nbreak"}[mu.s.n(6)]
	case c == 8:
		return time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	case c == 9 && depth < 2:
		l := make([]any, mu.s.n(3))
		for i := range l {
			l[i] = mu.value(depth + 1)
		}
		return l
	case c == 10 && depth < 2:
		return mu.table(depth + 1)
	case depth < 1:
		l := make([]map[string]any, 1+mu.s.n(2))
		for i := range l {
			l[i] = mu.table(depth + 1)
		}
		return l
	default:
		return int64(7)
	}
}

func (mu *mutation) table(depth int) map[string]any {
	m := map[string]any{}
	for i, n := 0, mu.s.n(3); i < n; i++ {
		m[mu.freshKey()] = mu.value(depth)
	}
	return m
}

// mutate changes m (the decoded document, at path) in place.
func (mu *mutation) mutate(path []string, m map[string]any) {
	for _, k := range sortedKeys(m) {
		sub := concat(path, []string{k})
		switch v := m[k].(type) {
		case map[string]any:
			switch mu.s.n(12) {
			case 0:
				delete(m, k)
				mu.removed = append(mu.removed, sub)
			case 1:
				m[k] = mu.value(1)
				mu.removed = append(mu.removed, sub)
			default:
				mu.mutate(sub, v)
			}
		case []map[string]any:
			mu.mutateArray(sub, m, k, v)
		case []any:
			if l, ok := tableList(v); ok {
				mu.mutateArray(sub, m, k, l)
				continue
			}
			mu.leaf(sub, m, k)
		default:
			mu.leaf(sub, m, k)
		}
	}
	if mu.s.p(25) {
		k := mu.freshKey()
		m[k] = mu.value(0)
		mu.set = append(mu.set, concat(path, []string{k}))
	}
	if mu.s.p(10) {
		k := mu.freshKey()
		m[k] = mu.table(1)
		mu.set = append(mu.set, concat(path, []string{k}))
	}
}

func (mu *mutation) leaf(sub []string, m map[string]any, k string) {
	switch mu.s.n(8) {
	case 0:
		delete(m, k)
		mu.removed = append(mu.removed, sub)
	case 1:
		nv := mu.value(0)
		if isSection(nv) != isSection(m[k]) {
			mu.removed = append(mu.removed, sub)
		}
		m[k] = nv
		mu.set = append(mu.set, sub)
	}
}

func (mu *mutation) mutateArray(sub []string, m map[string]any, k string, l []map[string]any) {
	switch mu.s.n(8) {
	case 0:
		delete(m, k)
		mu.removed = append(mu.removed, sub)
		return
	case 1:
		l = append(l, mu.table(1))
	case 2:
		i := mu.s.n(len(l))
		l = append(l[:i:i], l[i+1:]...)
	case 3, 4:
		mu.mutate(sub, l[mu.s.n(len(l))])
	case 5:
		if len(l) > 1 {
			l[0], l[len(l)-1] = l[len(l)-1], l[0]
		}
	default:
		return
	}
	m[k] = l
	mu.arrays = append(mu.arrays, sub)
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = deepCopy(e)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(x))
		for i, e := range x {
			out[i] = deepCopy(e).(map[string]any)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = deepCopy(e)
		}
		return out
	default:
		return v
	}
}

func hasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func underAny(path []string, set [][]string) bool {
	for _, s := range set {
		if hasPrefix(path, s) {
			return true
		}
	}
	return false
}

func relatedAny(path []string, set [][]string) bool {
	for _, s := range set {
		if hasPrefix(path, s) || hasPrefix(s, path) {
			return true
		}
	}
	return false
}

func encodeTarget(t testing.TB, m map[string]any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = "  "
	if err := enc.Encode(m); err != nil {
		t.Fatalf("encode target: %v", err)
	}
	return buf.Bytes()
}

// checkProperty is one round of the property: generate, change, patch,
// verify. It reports whether the patch fell back because BurntSushi
// misread a correct patch (see verifyError).
func checkProperty(t testing.TB, data []byte) (decoderFallback bool) {
	s := &src{data: data}
	g := &gen{s: s}
	orig := g.document()
	var old map[string]any
	if _, err := toml.Decode(orig, &old); err != nil {
		t.Fatalf("generator wrote invalid TOML: %v\n%s", err, orig)
	}
	old = orEmpty(old)
	mu := &mutation{s: s}
	next := deepCopy(old).(map[string]any)
	mu.mutate(nil, next)
	target := encodeTarget(t, next)
	var want map[string]any
	if _, err := toml.Decode(string(target), &want); err != nil {
		t.Fatalf("target does not decode: %v\n%s", err, target)
	}

	out, err := patch([]byte(orig), target, nil)
	var ve *verifyError
	if errors.As(err, &ve) && secondOpinion(ve.out, want) {
		// The patch is right — go-toml reads it as the target — but
		// BurntSushi misreads it, so falling back was correct.
		return true
	}
	if err != nil {
		t.Fatalf("patch failed: %v\n--- original\n%s\n--- target\n%s", err, orig, target)
	}
	var got map[string]any
	if _, err := toml.Decode(string(out), &got); err != nil {
		t.Fatalf("patched text does not decode: %v\n--- original\n%s\n--- patched\n%s", err, orig, out)
	}
	if !Equal(orEmpty(got), orEmpty(want)) {
		t.Fatalf("patched text decodes to %v, want %v\n--- original\n%s\n--- patched\n%s", got, want, orig, out)
	}
	if Equal(old, orEmpty(want)) && string(out) != orig {
		t.Fatalf("unchanged data but the text changed\n--- original\n%s\n--- patched\n%s", orig, out)
	}

	lost := append(append([][]string{}, mu.removed...), mu.arrays...)
	for _, c := range g.comments {
		skip := false
		for _, o := range c.owners {
			if underAny(o, lost) || (c.inValue && relatedAny(o, append(lost, mu.set...))) {
				skip = true
			}
		}
		if !skip && !strings.Contains(string(out), c.text+"\n") && !strings.Contains(string(out), c.text+"\r\n") && !strings.HasSuffix(string(out), c.text) {
			t.Fatalf("comment %q lost\n--- original\n%s\n--- patched\n%s", c.text, orig, out)
		}
	}
	touched := append(lost, mu.set...)
	for _, l := range g.lines {
		if relatedAny(l.path, touched) {
			continue
		}
		if !strings.Contains(string(out), l.text) {
			t.Fatalf("untouched line %q changed\n--- original\n%s\n--- patched\n%s", l.text, orig, out)
		}
	}

	// Saving the same data again is a byte-for-byte no-op.
	again, err := patch(out, target, nil)
	if err != nil || !bytes.Equal(again, out) {
		t.Fatalf("second patch not a no-op (err %v)\n--- first\n%s\n--- second\n%s", err, out, again)
	}
	return false
}

// secondOpinion decodes out with go-toml and reports whether it holds
// want — an independent check that a patch BurntSushi rejected was
// valid TOML for the right data.
func secondOpinion(out []byte, want map[string]any) bool {
	var got map[string]any
	if err := gotoml.Unmarshal(out, &got); err != nil {
		return false
	}
	return Equal(normTimes(orEmpty(got)), normTimes(orEmpty(want)))
}

// normTimes replaces datetimes with their RFC 3339 text: the two
// decoders name fixed zones differently.
func normTimes(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normTimes(e)
		}
		return out
	case []map[string]any, []any:
		l, _ := asList(x)
		out := make([]any, len(l))
		for i, e := range l {
			out[i] = normTimes(e)
		}
		return out
	case time.Time:
		return x.Format(time.RFC3339Nano)
	default:
		return v
	}
}
