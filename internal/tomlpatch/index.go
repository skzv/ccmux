package tomlpatch

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/pelletier/go-toml/v2/unstable"
)

// errShape is returned when the original document uses a structure
// the index doesn't model (or that the positional parser and the
// decoder disagree on). Patch then fails and the caller falls back to
// writing the full re-encode.
var errShape = errors.New("tomlpatch: unsupported document shape")

// doc is a positional index of a TOML document: which bytes define
// which key, and where each [table] section starts and ends.
type doc struct {
	src      []byte
	root     *table
	sections []*section // document order; sections[0] is the root (headerless) section
	comment  map[int]bool
	unit     string // indentation one nesting level adds, as the file itself writes it
	footer   int    // start of the comment block that ends the file apart from the last table (len(src) if none)
}

// section is one [table] or [[array]] header plus the lines up to the
// next header. The root section has no header and starts at offset 0.
type section struct {
	idx      int
	hdrStart int    // start of the header line (0 for the root section)
	hdrEnd   int    // start of the line after the header (0 for the root section)
	indent   string // whitespace before the header's '['
	kvs      []*kv  // key/value lines lexically in this section, in order
	deleted  bool
	role     blockRole // what the comment block directly above the header documents
	// arrayKept marks the first element of an array of tables that
	// outlives this patch (other elements survive or are added).
	arrayKept bool
}

// blockRole says what the comment lines directly above a header (no
// blank line between them and it) document, which decides whether they
// go when the section is deleted.
type blockRole int

const (
	// roleTable: a [table]'s own comment, deleted with it — unless it
	// opens the file, where it more likely describes the whole file.
	roleTable blockRole = iota
	// roleElem: an [[array]] element's own comment, deleted with it.
	roleElem
	// roleArrayDoc: the comment above an array's first element when no
	// other element has one of its own — it documents the array, so it
	// stays for the elements that remain.
	roleArrayDoc
)

// kv is one `key = value` expression.
type kv struct {
	sec       *section
	lineStart int // start of the line the key is on
	keyStart  int
	valStart  int
	valEnd    int
	lineEnd   int // just past the newline that ends the value's line (or EOF)
}

type tableKind int

const (
	kindRoot     tableKind = iota
	kindHeader             // defined by its own [path] header
	kindImplicit           // only exists because a deeper [path.sub] header passes through it
	kindDotted             // defined by dotted keys (a.b = 1) inside some section
	kindElem               // one element of an array of tables ([[path]])
)

// table is a TOML table and where its keys live in the text.
type table struct {
	kind    tableKind
	path    []string // full key path; array elements carry the array's path (no index)
	sec     *section // root/header/elem: the section holding its keys; dotted: the section its dotted keys are in
	prefix  []string // dotted only: the key parts, relative to the section's table, that reach it
	lastKV  *kv      // dotted only: the last line contributing a key under it
	entries map[string]*entry
}

// entry is what one key of a table holds: a value written on a
// key/value line, a sub-table, or an array of tables.
type entry struct {
	leaf *kv
	tbl  *table
	arr  []*table
}

func newTable(kind tableKind, path []string, sec *section) *table {
	return &table{kind: kind, path: path, sec: sec, entries: map[string]*entry{}}
}

// index parses src with go-toml's positional parser and builds the
// table tree the patcher edits against.
func index(src []byte) (*doc, error) {
	d := &doc{src: src, comment: map[int]bool{}}
	rootSec := &section{}
	d.sections = []*section{rootSec}
	d.root = newTable(kindRoot, nil, rootSec)
	cur := d.root

	p := unstable.Parser{KeepComments: true}
	p.Reset(src)
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.Comment:
			// Top-level comment expressions are always on a line of their
			// own (trailing comments hang off their expression instead).
			d.comment[lineStart(src, int(e.Raw.Offset))] = true
		case unstable.KeyValue:
			if err := d.addKV(cur, e); err != nil {
				return nil, err
			}
		case unstable.Table, unstable.ArrayTable:
			keys, first, last := keyParts(e)
			if len(keys) == 0 {
				return nil, errShape
			}
			hs := lineStart(src, first)
			sec := &section{
				idx:      len(d.sections),
				hdrStart: hs,
				hdrEnd:   lineEnd(src, last),
				indent:   leadingSpace(src[hs:first]),
			}
			d.sections = append(d.sections, sec)
			t, err := d.openTable(keys, e.Kind == unstable.ArrayTable, sec)
			if err != nil {
				return nil, err
			}
			cur = t
		default:
			return nil, errShape
		}
	}
	if err := p.Error(); err != nil {
		return nil, fmt.Errorf("tomlpatch: index: %w", err)
	}
	d.unit = d.detectUnit()
	d.assignRoles(d.root)
	d.footer = d.findFooter()
	return d, nil
}

// assignRoles sets the blockRole of every array element's section in
// t's subtree (sections default to roleTable). The first element's
// comment counts as its own only when another element has one too:
//
//	# first host        # my machines
//	[[host]]            [[host]]
//	...                 ...
//	# second host       [[host]]
//	[[host]]            ...
//
// On the left each host is documented; on the right the comment is
// about the list.
func (d *doc) assignRoles(t *table) {
	for _, e := range t.entries {
		if e.tbl != nil {
			d.assignRoles(e.tbl)
		}
		if len(e.arr) == 0 {
			continue
		}
		own := false
		for _, el := range e.arr[1:] {
			el.sec.role = roleElem
			if d.attachStart(el.sec) < el.sec.hdrStart {
				own = true
			}
		}
		e.arr[0].sec.role = roleArrayDoc
		if own {
			e.arr[0].sec.role = roleElem
		}
		for _, el := range e.arr {
			d.assignRoles(el)
		}
	}
}

// findFooter returns where a comment block that closes the file starts:
// comment lines at the very end (only comments and blank lines below
// them) with a blank line between them and the last table's content.
// Such a block stands apart from that table, so deleting the table
// leaves it. Returns len(src) when the file has no footer.
func (d *doc) findFooter() int {
	// Walk up over the trailing comment and blank lines.
	run := len(d.src)
	for run > 0 {
		prev := lineStart(d.src, run-1)
		if !d.comment[prev] && len(bytes.TrimSpace(d.src[prev:run])) != 0 {
			break
		}
		run = prev
	}
	// Comments right under the content belong to it; the footer is the
	// first comment after a blank line.
	blank := false
	for p := run; p < len(d.src); p = lineEnd(d.src, p) {
		switch {
		case d.comment[p]:
			if blank {
				return p
			}
		default:
			blank = true
		}
	}
	return len(d.src)
}

// keyParts returns the decoded parts of a (possibly dotted) key plus
// the offset where the key starts and where it ends.
func keyParts(e *unstable.Node) (keys []string, first, last int) {
	it := e.Key()
	first = -1
	for it.Next() {
		n := it.Node()
		keys = append(keys, string(n.Data))
		if first < 0 {
			first = int(n.Raw.Offset)
		}
		last = int(n.Raw.Offset + n.Raw.Length)
	}
	return keys, first, last
}

// openTable walks (creating implicit tables as needed) to the table a
// [keys] or [[keys]] header opens, and returns it.
func (d *doc) openTable(keys []string, array bool, sec *section) (*table, error) {
	t := d.root
	for i, k := range keys[:len(keys)-1] {
		e := t.entries[k]
		switch {
		case e == nil:
			nt := newTable(kindImplicit, clone(keys[:i+1]), nil)
			t.entries[k] = &entry{tbl: nt}
			t = nt
		case e.tbl != nil:
			t = e.tbl
		case len(e.arr) > 0:
			t = e.arr[len(e.arr)-1]
		default:
			return nil, errShape
		}
	}
	last := keys[len(keys)-1]
	e := t.entries[last]
	if array {
		nt := newTable(kindElem, clone(keys), sec)
		switch {
		case e == nil:
			t.entries[last] = &entry{arr: []*table{nt}}
		case len(e.arr) > 0:
			e.arr = append(e.arr, nt)
		default:
			return nil, errShape
		}
		return nt, nil
	}
	switch {
	case e == nil:
		nt := newTable(kindHeader, clone(keys), sec)
		t.entries[last] = &entry{tbl: nt}
		return nt, nil
	case e.tbl != nil && e.tbl.kind == kindImplicit:
		e.tbl.kind = kindHeader
		e.tbl.sec = sec
		return e.tbl, nil
	default:
		return nil, errShape
	}
}

// addKV records one key/value expression of section t.sec (t is the
// table the section's bare keys belong to).
func (d *doc) addKV(t *table, e *unstable.Node) error {
	src := d.src
	keys, first, last := keyParts(e)
	if len(keys) == 0 || first < 0 {
		return errShape
	}
	i := skipBlank(src, last)
	if i >= len(src) || src[i] != '=' {
		return errShape
	}
	valStart := skipBlank(src, i+1)
	valEnd := int(e.Raw.Offset + e.Raw.Length)
	if valEnd <= valStart || valEnd > len(src) {
		return errShape
	}
	sec := t.sec
	k := &kv{
		sec:       sec,
		lineStart: lineStart(src, first),
		keyStart:  first,
		valStart:  valStart,
		valEnd:    valEnd,
		lineEnd:   lineEnd(src, valEnd),
	}

	cur := t
	var chain []*table
	for j, part := range keys[:len(keys)-1] {
		en := cur.entries[part]
		switch {
		case en == nil:
			nt := newTable(kindDotted, concat(t.path, keys[:j+1]), sec)
			nt.prefix = clone(keys[:j+1])
			cur.entries[part] = &entry{tbl: nt}
			cur = nt
		case en.tbl != nil && en.tbl.kind == kindDotted && en.tbl.sec == sec:
			cur = en.tbl
		default:
			return errShape
		}
		chain = append(chain, cur)
	}
	lastKey := keys[len(keys)-1]
	if cur.entries[lastKey] != nil {
		return errShape
	}
	cur.entries[lastKey] = &entry{leaf: k}
	sec.kvs = append(sec.kvs, k)
	for _, c := range chain {
		c.lastKV = k
	}
	return nil
}

// detectUnit reports how far the file indents a table's keys past its
// header — "  " for files ccmux (BurntSushi's encoder) wrote, usually
// "" for hand-written ones — so inserted tables match.
func (d *doc) detectUnit() string {
	for _, s := range d.sections[1:] {
		if len(s.kvs) == 0 {
			continue
		}
		k := s.kvs[0]
		ind := string(d.src[k.lineStart:k.keyStart])
		if len(ind) > len(s.indent) && ind[:len(s.indent)] == s.indent {
			return ind[len(s.indent):]
		}
		return ""
	}
	return ""
}

// kvIndent is the whitespace before k's key.
func (d *doc) kvIndent(k *kv) string {
	return string(d.src[k.lineStart:k.keyStart])
}

// bodyEnd is where section s's own lines end: the start of the comment
// block attached to the next header, or EOF.
func (d *doc) bodyEnd(s *section) int {
	if s.idx+1 < len(d.sections) {
		return d.attachStart(d.sections[s.idx+1])
	}
	return len(d.src)
}

// attachStart walks back from section s's header over the comment
// lines directly above it (no blank line in between) — the comment
// that documents a table stays with it, and goes with it (see
// patcher.delStart).
func (d *doc) attachStart(s *section) int {
	p := s.hdrStart
	for p > 0 {
		prev := lineStart(d.src, p-1)
		if !d.comment[prev] {
			break
		}
		p = prev
	}
	return p
}

// contentEnd trims blank lines off the end of [lo, hi): the position
// just past the last non-blank line. hi must be a line start or EOF.
func (d *doc) contentEnd(lo, hi int) int {
	p := hi
	for p > lo {
		prev := lineStart(d.src, p-1)
		if prev < lo || len(bytes.TrimSpace(d.src[prev:p])) != 0 {
			break
		}
		p = prev
	}
	return p
}

// keyLineBefore reports whether the line ending at p (a line start) is
// a key, value or header line — neither blank nor a comment.
func (d *doc) keyLineBefore(p int) bool {
	if p == 0 {
		return false
	}
	prev := lineStart(d.src, p-1)
	return !d.comment[prev] && len(bytes.TrimSpace(d.src[prev:p])) != 0
}

// lineStart returns the offset of the start of the line containing
// byte off.
func lineStart(src []byte, off int) int {
	if off > len(src) {
		off = len(src)
	}
	return bytes.LastIndexByte(src[:off], '\n') + 1
}

// lineEnd returns the offset just past the first newline at or after
// off, or len(src).
func lineEnd(src []byte, off int) int {
	if off >= len(src) {
		return len(src)
	}
	i := bytes.IndexByte(src[off:], '\n')
	if i < 0 {
		return len(src)
	}
	return off + i + 1
}

func skipBlank(src []byte, i int) int {
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return i
}

func leadingSpace(b []byte) string {
	n := 0
	for n < len(b) && (b[n] == ' ' || b[n] == '\t') {
		n++
	}
	return string(b[:n])
}

func clone(s []string) []string {
	return append([]string(nil), s...)
}

func concat(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}
