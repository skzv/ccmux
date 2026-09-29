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
// value in place, deleting a removed key's line (or a removed table,
// with the comment written directly above it), and adding new keys at
// the end of their table (creating the [table] header when missing).
// The elements of an [[array]] are matched by identity, not position,
// so each keeps its own comments when others are added or removed.
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
	"slices"
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

// patchArray edits the [[path]] elements of an array of tables. Each
// new element that is an old one (see alignTables) is patched in place,
// so the comments in and above it stay with it; old elements with no
// counterpart are deleted, comment included, and new ones inserted.
func (p *patcher) patchArray(elems []*table, path []string, ol, nl []map[string]any) error {
	m, n := len(ol), len(nl)
	// The array outlives the patch (nl is never empty): a comment that
	// documents it stays even if its first element goes.
	elems[0].sec.arrayKept = true
	pairs := append(alignTables(ol, nl), [2]int{m, n}) // sentinel closing the last gap

	kept := -1 // last surviving element before the current gap
	oi, nj := 0, 0
	for _, pr := range pairs {
		mi, mj := pr[0], pr[1]
		for x := oi; x < mi; x++ {
			p.deleteTable(elems[x])
		}
		if nj < mj {
			var sb strings.Builder
			for x := nj; x < mj; x++ {
				if err := p.renderSection(&sb, path, nl[x], true); err != nil {
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
			case mi < m:
				// New elements before every surviving one: insert above
				// the next element and its own comment (below a comment
				// that documents the whole array).
				s := elems[mi].sec
				pos := p.d.attachStart(s)
				if s.role == roleArrayDoc {
					pos = s.hdrStart
				}
				p.add(pos, pos, prioSection, sb.String()+p.nl)
			default:
				// Nothing survives: the new elements take the old ones'
				// place, set apart from whatever follows them.
				pos, text := p.delStart(elems[0].sec), sb.String()
				if secs := p.ownSections(elems[m-1]); secs[len(secs)-1].idx < len(p.d.sections)-1 {
					text += p.nl
				}
				p.add(pos, pos, prioSection, text)
			}
		}
		if mi < m {
			if err := p.patchTable(elems[mi], path, ol[mi], nl[mj]); err != nil {
				return err
			}
			kept = mi
		}
		oi, nj = mi+1, mj+1
	}
	return nil
}

// alignTables decides which old element of an array of tables each new
// element is, so a save that changed some elements (and added or
// removed others) edits each in place rather than rewriting its
// neighbour into it — which would leave every comment above the wrong
// element. It returns the matched (old, new) index pairs in order, both
// indexes increasing.
//
// Two elements may match when:
//   - the array has identity keys (identityKeys: e.g. `name` for
//     ccmux's [[host]]) and they agree on one of them; or else
//   - most of the keys they both have hold equal values
//     (similarityPct >= 50). Keys only one side has don't count
//     against it, so an element whose only change is keys added (a
//     save filling in defaults) or removed still matches itself.
//
// Among the orderings allowed, it keeps the most elements, then the
// most similar ones. Without identity keys, an equal number of
// unmatched old and new elements between two matches are paired in
// order — most likely each edited in place — as they were before
// identity matching existed.
func alignTables(ol, nl []map[string]any) [][2]int {
	m, n := len(ol), len(nl)
	ids := identityKeys(ol, nl)
	// A matched pair is worth more than any difference in similarity,
	// so the alignment keeps the most elements first.
	const pairWorth = 4096
	score := make([][]int, m)
	for i := range score {
		score[i] = make([]int, n)
		for j := range score[i] {
			if s, ok := matchScore(ol[i], nl[j], ids); ok {
				score[i][j] = pairWorth + s
			}
		}
	}
	// best[i][j] is the highest total score aligning ol[i:] with nl[j:].
	best := make([][]int, m+1)
	for i := range best {
		best[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			b := max(best[i+1][j], best[i][j+1])
			if s := score[i][j]; s > 0 {
				b = max(b, best[i+1][j+1]+s)
			}
			best[i][j] = b
		}
	}
	var pairs [][2]int
	for i, j := 0, 0; i < m && j < n; {
		switch s := score[i][j]; {
		case s > 0 && best[i][j] == best[i+1][j+1]+s:
			pairs = append(pairs, [2]int{i, j})
			i++
			j++
		case best[i][j] == best[i+1][j]:
			i++
		default:
			j++
		}
	}
	if len(ids) > 0 {
		return pairs
	}
	var out [][2]int
	pi, pj := -1, -1
	for _, pr := range append(pairs, [2]int{m, n}) {
		if gap := pr[0] - pi - 1; gap > 0 && gap == pr[1]-pj-1 {
			for x := 1; x <= gap; x++ {
				out = append(out, [2]int{pi + x, pj + x})
			}
		}
		if pr[0] < m {
			out = append(out, pr)
		}
		pi, pj = pr[0], pr[1]
	}
	return out
}

// matchScore reports whether old element o and new element n may be
// the same element, and how alike they are (higher is more alike).
func matchScore(o, n map[string]any, ids []string) (int, bool) {
	sim, common := similarityPct(o, n)
	switch {
	case len(ids) > 0:
		if !slices.ContainsFunc(ids, func(k string) bool { return Equal(o[k], n[k]) }) {
			return 0, false
		}
	case common == 0 && len(o)+len(n) > 0, sim < 50:
		// Nothing in common, or mostly different values.
		return 0, false
	}
	s := 2 * sim
	if Equal(o, n) {
		s++ // an untouched element beats one with added keys
	}
	return s, true
}

// similarityPct is the percentage of the keys both o and n have whose
// values are equal (100 when they share no keys), and how many keys
// they share.
func similarityPct(o, n map[string]any) (pct, common int) {
	same := 0
	for k, v := range o {
		if w, ok := n[k]; ok {
			common++
			if Equal(v, w) {
				same++
			}
		}
	}
	if common == 0 {
		return 100, 0
	}
	return same * 100 / common, common
}

// identityKeys lists the keys that name an array's elements: held by
// every element before and after the change, as a string or integer
// that differs between any two elements on the same side — `name` in
// ccmux's [[host]] list. With at most one element on each side, nothing
// tells a naming key from any other, so there are none.
func identityKeys(ol, nl []map[string]any) []string {
	if len(ol) < 2 && len(nl) < 2 {
		return nil
	}
	var ids []string
	for k := range ol[0] {
		if namesEach(ol, k) && namesEach(nl, k) {
			ids = append(ids, k)
		}
	}
	sort.Strings(ids)
	return ids
}

// namesEach reports whether every element of l holds a distinct string
// or integer at k.
func namesEach(l []map[string]any, k string) bool {
	seen := make(map[any]bool, len(l))
	for _, m := range l {
		v := m[k]
		switch v.(type) {
		case string, int64:
		default:
			return false
		}
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
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
		// Every sub-table is being deleted; where a deletion starts is
		// always an edit boundary.
		return p.delStart(secs[0]), nil
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
		return p.delStart(secs[0]), false, nil
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
// takes the comment attached to its header (see delStart) and its lines
// up to the comment attached to the next header, which stays with that
// table — or, for the last section, up to the file's footer comment.
func (p *patcher) flushSections() {
	secs := p.d.sections
	for _, s := range secs {
		if !s.deleted {
			continue
		}
		start, end := p.delStart(s), p.d.bodyEnd(s)
		switch {
		case s.idx == len(secs)-1 && p.d.footer < len(p.d.src):
			// Keep the footer and the blank line that sets it apart.
			end = p.d.contentEnd(start, p.d.footer)
		case end < len(p.d.src) && p.d.keyLineBefore(start):
			// The section followed the one above with no blank line; the
			// blank lines that set it apart from the next now set that
			// one apart instead.
			end = p.d.contentEnd(start, end)
		}
		p.add(start, end, prioLine, "")
	}
	p.trimTail()
}

// delStart is where deleting section s starts: above its header when
// the comment block attached to the header goes with it, else at the
// header. A table's comment goes unless it opens the file (the file's
// own header comment, as likely as not); an array element's own comment
// always goes; a comment documenting the whole array goes only with
// the array's last element.
func (p *patcher) delStart(s *section) int {
	a := p.d.attachStart(s)
	var keep bool
	switch s.role {
	case roleTable:
		keep = a == 0
	case roleArrayDoc:
		keep = s.arrayKept || a == 0
	case roleElem:
		keep = false
	}
	if keep {
		return s.hdrStart
	}
	return a
}

// trimTail removes the blank lines that would end the file once its
// last sections are deleted (they separated those sections from the
// text above) — or that would double the ones setting the footer
// apart. Text inserted before them or at EOF brings its own separation;
// anything inserted in between (in place of a deleted section) keeps
// them.
func (p *patcher) trimTail() {
	secs := p.d.sections
	k := len(secs) - 1
	if k == 0 || !secs[k].deleted {
		return
	}
	for k > 1 && secs[k-1].deleted {
		k--
	}
	start := p.delStart(secs[k])
	from := p.d.contentEnd(0, start)
	if from == start {
		return
	}
	for _, e := range p.edits {
		if e.start == e.end && e.start > from && e.start < len(p.d.src) {
			return
		}
	}
	p.add(from, start, prioLine, "")
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
