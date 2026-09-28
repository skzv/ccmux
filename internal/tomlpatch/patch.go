// Package tomlpatch rewrites a TOML file the user also edits by hand
// without trampling their formatting.
//
// ccmux saves its own config.toml and Codex's config.toml by encoding a
// Go value from scratch. That produces the right data but drops every
// comment, blank line, key order and quoting choice in the file being
// replaced. Patch keeps the original text instead and edits only what
// changed: it decodes both the original and the freshly encoded target,
// diffs them by key path, and applies the differences to the original
// bytes at the positions go-toml's parser reports — replacing a changed
// value in place, deleting a removed key's line, and adding new keys at
// the end of their table (creating the [table] header when missing).
//
// The result always decodes to exactly what the target decodes to:
// Patch re-decodes its own output and compares before returning, and
// reports an error instead whenever an edit can't be made safely.
// Callers then write the target as-is — the old full-rewrite behaviour
// — so a patch can lose formatting but never data.
package tomlpatch

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// errConflict is returned when two edits would touch the same bytes.
var errConflict = errors.New("tomlpatch: overlapping edits")

// afterApply, when set by tests, rewrites the patched bytes before the
// verification step — the seam that proves a bad patch is caught.
var afterApply func([]byte) []byte

// Patch returns original rewritten so that it decodes to the same data
// as target, changing as little of original's text as possible.
//
// target is normally the full re-encode of the new value (what the
// caller would otherwise write). Comments, blank lines, key order,
// indentation and the formatting of every value that didn't change
// survive byte-for-byte. On error the caller should write target
// unchanged: Patch errs rather than guess whenever original can't be
// parsed, uses a shape the patcher doesn't model, or the patched text
// fails to decode back to target's data.
func Patch(original, target []byte) ([]byte, error) {
	return PatchOrdered(original, target, nil)
}

// PatchOrdered is Patch with a say in where added keys go: they follow
// the order they have in order (a TOML document — typically the
// encoding of the typed struct, when target is a re-encoded map and so
// alphabetical), then the order they have in target. A nil order is
// Patch.
func PatchOrdered(original, target, order []byte) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("tomlpatch: internal error: %v", r)
		}
	}()
	return patch(original, target, order)
}

func patch(original, target, order []byte) ([]byte, error) {
	var oldDoc, newDoc map[string]any
	if _, err := toml.Decode(string(original), &oldDoc); err != nil {
		return nil, fmt.Errorf("tomlpatch: parse original: %w", err)
	}
	md, err := toml.Decode(string(target), &newDoc)
	if err != nil {
		return nil, fmt.Errorf("tomlpatch: parse target: %w", err)
	}
	metas := []toml.MetaData{md}
	if order != nil {
		var ignored map[string]any
		omd, err := toml.Decode(string(order), &ignored)
		if err != nil {
			return nil, fmt.Errorf("tomlpatch: parse order: %w", err)
		}
		metas = []toml.MetaData{omd, md}
	}
	rank := ranks(metas...)
	oldDoc, newDoc = orEmpty(oldDoc), orEmpty(newDoc)
	if Equal(oldDoc, newDoc) {
		// Nothing changed: keep the file byte-for-byte, whatever its
		// shape.
		return original, nil
	}
	if len(bytes.TrimSpace(original)) == 0 {
		// Nothing to preserve.
		return target, nil
	}
	// go-toml's parser rejects the UTF-8 byte order mark the decoder
	// accepts; patch the body and put the mark back.
	if bom := []byte("\uFEFF"); bytes.HasPrefix(original, bom) {
		out, err := patchBody(original[len(bom):], rank, oldDoc, newDoc)
		if err != nil {
			return nil, err
		}
		return append(bom, out...), nil
	}
	return patchBody(original, rank, oldDoc, newDoc)
}

func patchBody(original []byte, rank map[string]int, oldDoc, newDoc map[string]any) ([]byte, error) {
	d, err := index(original)
	if err != nil {
		return nil, err
	}
	p := &patcher{d: d, rank: rank, nl: "\n"}
	if i := bytes.IndexByte(original, '\n'); i > 0 && original[i-1] == '\r' {
		p.nl = "\r\n"
	}
	if err := p.patchTable(d.root, nil, oldDoc, newDoc); err != nil {
		return nil, err
	}
	p.flushSections()
	out, err := apply(original, p.edits, p.nl)
	if err != nil {
		return nil, err
	}
	if afterApply != nil {
		out = afterApply(out)
	}
	if err := verify(out, newDoc); err != nil {
		return nil, &verifyError{out: out, err: err}
	}
	return out, nil
}

// verifyError is a patch that was built but didn't decode back to the
// target. Tests read out to tell a patcher bug from a decoder one:
// BurntSushi/toml v1.6.0 misreads some valid documents (dotted keys in
// one [[array]] element after the same key held a value in an earlier
// element), and the fallback is the right call for those too.
type verifyError struct {
	out []byte
	err error
}

func (e *verifyError) Error() string { return e.err.Error() }
func (e *verifyError) Unwrap() error { return e.err }

// verify re-decodes the patched text and checks it holds want.
func verify(out []byte, want map[string]any) error {
	var got map[string]any
	if _, err := toml.Decode(string(out), &got); err != nil {
		return fmt.Errorf("tomlpatch: patched text does not parse: %w", err)
	}
	if !Equal(orEmpty(got), orEmpty(want)) {
		return errors.New("tomlpatch: patched text does not decode to the target")
	}
	return nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Edit priorities for insertions at the same offset: a key line always
// goes before a new section inserted at that spot, or the key would
// land under the new header.
const (
	prioLine = iota
	prioSection
)

type edit struct {
	start, end int
	prio, seq  int
	text       string
}

type patcher struct {
	d     *doc
	rank  map[string]int
	nl    string
	edits []edit
}

func (p *patcher) add(start, end, prio int, text string) {
	p.edits = append(p.edits, edit{start: start, end: end, prio: prio, seq: len(p.edits), text: text})
}

type addition struct {
	key string
	val any
}

// patchTable edits table t (at path) so it holds nv instead of ov.
func (p *patcher) patchTable(t *table, path []string, ov, nv map[string]any) error {
	var lines, sections []addition
	queue := func(k string, v any) {
		if isSection(v) && t.kind != kindDotted {
			sections = append(sections, addition{k, v})
		} else {
			lines = append(lines, addition{k, v})
		}
	}
	for _, k := range p.orderedKeys(path, nv) {
		n := nv[k]
		o, had := ov[k]
		e := t.entries[k]
		if !had {
			if e != nil {
				return errShape
			}
			queue(k, n)
			continue
		}
		if e == nil {
			return errShape
		}
		if Equal(o, n) {
			continue
		}
		sub := concat(path, []string{k})
		switch {
		case e.leaf != nil:
			s, err := p.renderValue(sub, n)
			if err != nil {
				return err
			}
			p.add(e.leaf.valStart, e.leaf.valEnd, prioLine, s)
		case e.tbl != nil:
			om, ok := o.(map[string]any)
			if !ok {
				return errShape
			}
			// A table defined only by dotted keys or deeper headers would
			// vanish from the text once empty; write it afresh instead.
			if nm, ok := n.(map[string]any); ok && (len(nm) > 0 || e.tbl.kind == kindHeader) {
				if err := p.patchTable(e.tbl, sub, om, nm); err != nil {
					return err
				}
				continue
			}
			p.deleteTable(e.tbl)
			queue(k, n)
		default:
			ol, ok := tableList(o)
			if !ok || len(ol) != len(e.arr) {
				return errShape
			}
			if nl, ok := tableList(n); ok {
				if err := p.patchArray(e.arr, sub, ol, nl); err != nil {
					return err
				}
				continue
			}
			for _, el := range e.arr {
				p.deleteTable(el)
			}
			queue(k, n)
		}
	}
	for _, k := range sortedKeys(ov) {
		if _, keep := nv[k]; keep {
			continue
		}
		e := t.entries[k]
		switch {
		case e == nil:
			return errShape
		case e.leaf != nil:
			p.deleteLine(e.leaf)
		case e.tbl != nil:
			p.deleteTable(e.tbl)
		default:
			for _, el := range e.arr {
				p.deleteTable(el)
			}
		}
	}
	if err := p.insertLines(t, path, lines); err != nil {
		return err
	}
	return p.insertSections(t, path, sections)
}

// patchArray edits the [[path]] elements of an array of tables. It
// keeps elements that are unchanged, patches changed ones in place
// (so comments inside them survive), and deletes or inserts the rest,
// aligning old and new by their longest common subsequence.
func (p *patcher) patchArray(elems []*table, path []string, ol, nl []map[string]any) error {
	m, n := len(ol), len(nl)
	// lcs[i][j] = LCS length of ol[i:] and nl[j:].
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if Equal(ol[i], nl[j]) {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type pair struct{ i, j int }
	var matches []pair
	for i, j := 0, 0; i < m && j < n; {
		switch {
		case Equal(ol[i], nl[j]):
			matches = append(matches, pair{i, j})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			i++
		default:
			j++
		}
	}
	matches = append(matches, pair{m, n}) // sentinel closing the last gap

	kept := -1 // last surviving element before the current gap
	oi, nj := 0, 0
	for _, mt := range matches {
		olds := mt.i - oi
		news := mt.j - nj
		paired := min(olds, news)
		for x := 0; x < paired; x++ {
			if err := p.patchTable(elems[oi+x], path, ol[oi+x], nl[nj+x]); err != nil {
				return err
			}
			kept = oi + x
		}
		for x := paired; x < olds; x++ {
			p.deleteTable(elems[oi+x])
		}
		if paired < news {
			var sb strings.Builder
			for x := paired; x < news; x++ {
				if err := p.renderSection(&sb, path, nl[nj+x], true); err != nil {
					return err
				}
			}
			switch {
			case kept >= 0:
				pos, _, err := p.sectionSlot(elems[kept])
				if err != nil {
					return err
				}
				p.add(pos, pos, prioSection, p.nl+sb.String())
			case mt.i < m:
				// New elements before every surviving one: insert above
				// the next element (and the comment documenting it).
				pos := p.d.attachStart(elems[mt.i].sec)
				p.add(pos, pos, prioSection, sb.String()+p.nl)
			default:
				return errShape
			}
		}
		if mt.i < m {
			kept = mt.i
		}
		oi, nj = mt.i+1, mt.j+1
	}
	return nil
}

// insertLines adds new keys to table t as `key = value` lines at the
// end of its key/value lines.
func (p *patcher) insertLines(t *table, path []string, adds []addition) error {
	if len(adds) == 0 {
		return nil
	}
	d := p.d
	render := func(indent string, prefix []string) (string, error) {
		var sb strings.Builder
		for _, a := range adds {
			line, err := p.renderLine(indent, concat(prefix, []string{a.key}), concat(path, []string{a.key}), a.val)
			if err != nil {
				return "", err
			}
			sb.WriteString(line)
		}
		return sb.String(), nil
	}
	switch t.kind {
	case kindDotted:
		text, err := render(d.kvIndent(t.lastKV), t.prefix)
		if err != nil {
			return err
		}
		p.add(t.lastKV.lineEnd, t.lastKV.lineEnd, prioLine, text)
	case kindImplicit:
		// The table exists only through deeper headers ([a.b] with no
		// [a]): give it a header of its own, above its first sub-table.
		pos, err := p.headerSlot(t)
		if err != nil {
			return err
		}
		var sb strings.Builder
		if err := p.renderSection(&sb, path, additionsMap(adds), false); err != nil {
			return err
		}
		p.add(pos, pos, prioSection, sb.String()+p.nl)
	default: // root, header, elem
		s := t.sec
		switch {
		case len(s.kvs) > 0:
			last := s.kvs[len(s.kvs)-1]
			text, err := render(d.kvIndent(last), nil)
			if err != nil {
				return err
			}
			p.add(last.lineEnd, last.lineEnd, prioLine, text)
		case t.kind != kindRoot:
			text, err := render(s.indent+d.unit, nil)
			if err != nil {
				return err
			}
			p.add(s.hdrEnd, s.hdrEnd, prioLine, text)
		case len(d.sections) > 1:
			// Root keys must precede the first header.
			text, err := render("", nil)
			if err != nil {
				return err
			}
			pos := d.attachStart(d.sections[1])
			p.add(pos, pos, prioLine, text+p.nl)
		default:
			text, err := render("", nil)
			if err != nil {
				return err
			}
			pos := d.contentEnd(0, len(d.src))
			p.add(pos, pos, prioLine, text)
		}
	}
	return nil
}

func additionsMap(adds []addition) map[string]any {
	m := make(map[string]any, len(adds))
	for _, a := range adds {
		m[a.key] = a.val
	}
	return m
}

// insertSections adds new sub-tables / arrays of tables of t as
// [path.key] sections after the last section of t's subtree (at EOF
// for the root).
func (p *patcher) insertSections(t *table, path []string, adds []addition) error {
	if len(adds) == 0 {
		return nil
	}
	var blocks []string
	for _, a := range adds {
		sub := concat(path, []string{a.key})
		var sb strings.Builder
		if m, ok := a.val.(map[string]any); ok {
			if err := p.renderSection(&sb, sub, m, false); err != nil {
				return err
			}
			blocks = append(blocks, sb.String())
			continue
		}
		elems, _ := tableList(a.val)
		for _, e := range elems {
			sb.Reset()
			if err := p.renderSection(&sb, sub, e, true); err != nil {
				return err
			}
			blocks = append(blocks, sb.String())
		}
	}
	pos, after := p.docEnd(), true
	if t.kind != kindRoot {
		var err error
		if pos, after, err = p.sectionSlot(t); err != nil {
			return err
		}
	}
	// A blank line separates each new section from what it follows (or,
	// when it has to go above existing sections, from what it precedes).
	var text string
	if after {
		text = p.nl + strings.Join(blocks, p.nl)
		if pos == 0 {
			text = strings.TrimPrefix(text, p.nl)
		}
	} else {
		text = strings.Join(blocks, p.nl) + p.nl
	}
	p.add(pos, pos, prioSection, text)
	return nil
}

// ownSections lists, in document order, the sections that belong to
// t's subtree: its own header section and every descendant's.
func (p *patcher) ownSections(t *table) []*section {
	var out []*section
	var walk func(*table)
	walk = func(t *table) {
		if (t.kind == kindHeader || t.kind == kindElem) && t.sec != nil {
			out = append(out, t.sec)
		}
		for _, e := range t.entries {
			if e.tbl != nil {
				walk(e.tbl)
			}
			for _, el := range e.arr {
				walk(el)
			}
		}
	}
	walk(t)
	sort.Slice(out, func(i, j int) bool { return out[i].idx < out[j].idx })
	return out
}

// docEnd is where a new top-level section goes: past the last
// non-blank line, or at EOF when the last section is being deleted
// (its deletion runs to EOF).
func (p *patcher) docEnd() int {
	last := p.d.sections[len(p.d.sections)-1]
	if last.deleted {
		return len(p.d.src)
	}
	return p.d.contentEnd(last.hdrStart, len(p.d.src))
}

// headerSlot is where a header for implicit table t goes: above its
// first surviving sub-table (and the comment documenting it).
func (p *patcher) headerSlot(t *table) (int, error) {
	secs := p.ownSections(t)
	for _, s := range secs {
		if !s.deleted {
			return p.d.attachStart(s), nil
		}
	}
	if len(secs) > 0 {
		// Every sub-table is being deleted; a deleted header's start is
		// always an edit boundary.
		return secs[0].hdrStart, nil
	}
	return 0, errShape
}

// sectionSlot is where a new section that belongs under t goes: just
// past the last non-blank line of the last surviving section in t's
// subtree (after=true). If none survives, it goes where the first one
// was (after=false: it precedes whatever follows).
func (p *patcher) sectionSlot(t *table) (pos int, after bool, err error) {
	secs := p.ownSections(t)
	for i := len(secs) - 1; i >= 0; i-- {
		if s := secs[i]; !s.deleted {
			return p.d.contentEnd(s.hdrStart, p.d.bodyEnd(s)), true, nil
		}
	}
	if len(secs) > 0 {
		return secs[0].hdrStart, false, nil
	}
	return 0, false, errShape
}

// deleteTable removes table t: its sections, and any of its keys that
// live on dotted-key lines in some other section.
func (p *patcher) deleteTable(t *table) {
	secs := p.ownSections(t)
	own := make(map[*section]bool, len(secs))
	for _, s := range secs {
		s.deleted = true
		own[s] = true
	}
	var walk func(*table)
	walk = func(t *table) {
		for _, e := range t.entries {
			switch {
			case e.leaf != nil:
				if !own[e.leaf.sec] {
					p.deleteLine(e.leaf)
				}
			case e.tbl != nil:
				walk(e.tbl)
			default:
				for _, el := range e.arr {
					walk(el)
				}
			}
		}
	}
	walk(t)
}

func (p *patcher) deleteLine(k *kv) {
	p.add(k.lineStart, k.lineEnd, prioLine, "")
}

// flushSections turns deleted sections into edits. A deleted section
// takes its lines up to the comment attached to the next header; when
// the next section goes too, that comment goes with them.
func (p *patcher) flushSections() {
	secs := p.d.sections
	for i, s := range secs {
		if !s.deleted {
			continue
		}
		end := p.d.bodyEnd(s)
		if i+1 < len(secs) && secs[i+1].deleted {
			end = secs[i+1].hdrStart
		}
		p.add(s.hdrStart, end, prioLine, "")
	}
}

// apply splices the edits into src. Insertions land at line starts; if
// one lands at the end of a file with no trailing newline, a newline is
// written first.
func apply(src []byte, edits []edit, nl string) ([]byte, error) {
	sort.SliceStable(edits, func(i, j int) bool {
		a, b := edits[i], edits[j]
		if a.start != b.start {
			return a.start < b.start
		}
		if a.end != b.end {
			return a.end < b.end
		}
		if a.prio != b.prio {
			return a.prio < b.prio
		}
		return a.seq < b.seq
	})
	var out bytes.Buffer
	out.Grow(len(src))
	cur := 0
	for _, e := range edits {
		if e.start < cur || e.end < e.start || e.end > len(src) {
			return nil, errConflict
		}
		out.Write(src[cur:e.start])
		if e.start == e.end && e.text != "" {
			if b := out.Bytes(); len(b) > 0 && b[len(b)-1] != '\n' {
				out.WriteString(nl)
			}
		}
		out.WriteString(e.text)
		cur = e.end
	}
	out.Write(src[cur:])
	return out.Bytes(), nil
}
