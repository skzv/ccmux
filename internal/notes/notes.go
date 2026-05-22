// Package notes is the on-disk operations layer for the TUI's Notes tab.
// One project's notes vault == every markdown file under the project
// root, grouped by the folder it lives in. No global vault, no required
// sync service — plain markdown on the filesystem is the source of
// truth. New notes created from the TUI still land under docs/ (the
// canonical home), but the listing surfaces README.md, CLAUDE.md,
// openspec/, and anything else, not just docs/.
package notes

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Vault is the markdown tree for one project — rooted at the project
// directory itself, so List() and Search() see every .md file, not
// just the docs/ subtree.
type Vault struct {
	Root string // absolute path to the project directory
}

// Open returns a Vault rooted at the project directory. The New*
// methods create docs/ on first write; the directory tree does not
// need to exist yet.
func Open(projectPath string) Vault {
	return Vault{Root: projectPath}
}

// Section identifies one of the three canonical docs/ subdirectories
// the New* quick-actions create into. It no longer drives the listing
// (which groups by actual folder) — only note creation.
type Section int

const (
	SectionSpecs Section = iota
	SectionArchitecture
	SectionAgentLogs
	SectionOther
)

func (s Section) Dir() string {
	switch s {
	case SectionSpecs:
		return "01_Specs"
	case SectionArchitecture:
		return "02_Architecture"
	case SectionAgentLogs:
		return "03_Agent_Logs"
	}
	return ""
}

func (s Section) Label() string {
	switch s {
	case SectionSpecs:
		return "Specs"
	case SectionArchitecture:
		return "Architecture"
	case SectionAgentLogs:
		return "Agent Logs"
	}
	return "Other"
}

// Entry is one markdown file in the vault listing.
type Entry struct {
	Path     string    // absolute path on disk
	Rel      string    // slash-separated path relative to the project root (e.g. "docs/01_Specs/00_Vision.md")
	Dir      string    // slash-separated directory portion of Rel ("" for a root-level file)
	Display  string    // short, human-readable label for the TUI
	Modified time.Time // mtime, for sorting
}

// List returns every markdown file under the project, sorted by
// containing directory (root-level files first, then folders
// lexically) and then by filename. Agent-log folders sort newest-first
// since the filename is the date. Version-control, dependency, and
// build-output directories are pruned so the listing reflects the
// project's own notes, not vendored README noise.
func (v Vault) List() ([]Entry, error) {
	if _, err := os.Stat(v.Root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	err := filepath.WalkDir(v.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != v.Root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(v.Root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, _ := d.Info()
		var mod time.Time
		if info != nil {
			mod = info.ModTime()
		}
		out = append(out, Entry{
			Path:     path,
			Rel:      rel,
			Dir:      dirOf(rel),
			Display:  displayFor(rel),
			Modified: mod,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir < out[j].Dir
		}
		// Inside an Agent Logs folder, newest-first is friendlier.
		if filepath.Base(out[i].Dir) == SectionAgentLogs.Dir() {
			return out[i].Rel > out[j].Rel
		}
		return out[i].Rel < out[j].Rel
	})
	return out, nil
}

// Read returns the bytes of the file at `rel` (a slash-separated path
// relative to the project root). Wraps the canonical filesystem error.
func (v Vault) Read(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(v.Root, filepath.FromSlash(rel)))
}

// NewAgentLog returns the path to today's agent log, creating it from a
// template if it doesn't exist yet. If `extraSession` is non-empty,
// appends a session-start entry to the log immediately after the header
// (used by ccmux when a new Claude session is launched for this project).
func (v Vault) NewAgentLog(extraSession string) (string, bool, error) {
	dir := filepath.Join(v.Root, "docs", SectionAgentLogs.Dir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, today+".md")
	created := false
	if _, err := os.Stat(path); os.IsNotExist(err) {
		body := fmt.Sprintf(`---
date: %s
project: %s
sessions: []
---

# Agent Log — %s

`, today, filepath.Base(v.Root), today)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return "", false, err
		}
		created = true
	}
	if extraSession != "" {
		ts := time.Now().Format("15:04")
		entry := fmt.Sprintf("\n## %s — Started session `%s`\n\n", ts, extraSession)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return path, created, err
		}
		defer f.Close()
		if _, err := f.WriteString(entry); err != nil {
			return path, created, err
		}
	}
	return path, created, nil
}

// NewSpec creates a new spec file under docs/01_Specs/ with an
// auto-incremented prefix (00_, 01_, 02_, …). Title is slugified for
// the filename and used verbatim as the H1.
func (v Vault) NewSpec(title string) (string, error) {
	return v.newNumberedNote(SectionSpecs, title, specTemplate)
}

// NewADR creates a new ADR under docs/02_Architecture/, same numbering
// scheme as NewSpec.
func (v Vault) NewADR(title string) (string, error) {
	return v.newNumberedNote(SectionArchitecture, title, adrTemplate)
}

func (v Vault) newNumberedNote(sec Section, title string, tmpl func(id int, title, date string) string) (string, error) {
	if strings.TrimSpace(title) == "" {
		return "", fmt.Errorf("title required")
	}
	dir := filepath.Join(v.Root, "docs", sec.Dir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	id := nextNumberedID(dir)
	slug := slugify(title)
	filename := fmt.Sprintf("%02d_%s.md", id, slug)
	path := filepath.Join(dir, filename)
	body := tmpl(id, title, time.Now().Format("2006-01-02"))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func specTemplate(id int, title, date string) string {
	return fmt.Sprintf(`---
id: %02d
title: %s
status: draft
created: %s
---

# %s

## Problem

## Approach

## Open Questions
`, id, title, date, title)
}

func adrTemplate(id int, title, date string) string {
	return fmt.Sprintf(`---
id: %02d
title: %s
status: proposed
created: %s
---

# %s

## Status

Proposed — %s

## Context

## Decision

## Consequences
`, id, title, date, title, date)
}

// nextNumberedID returns the next NN_ prefix to use in `dir`. Scans
// existing entries, picks max+1, defaults to 0 for empty dirs.
func nextNumberedID(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	rx := regexp.MustCompile(`^(\d{2,})_`)
	max := -1
	for _, e := range entries {
		m := rx.FindStringSubmatch(e.Name())
		if len(m) < 2 {
			continue
		}
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > max {
			max = n
		}
	}
	return max + 1
}

// skipDir reports whether a directory should be pruned from the vault
// walk. Hidden directories (.git, .obsidian, .ccmux) plus the usual
// dependency and build-output trees hold vendored markdown that would
// bury the project's own notes.
func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist", "build", "target":
		return true
	}
	return false
}

// dirOf returns the slash-separated directory portion of a
// vault-relative path, or "" when the file sits at the project root.
func dirOf(rel string) string {
	d := filepath.ToSlash(filepath.Dir(rel))
	if d == "." {
		return ""
	}
	return d
}

// displayFor strips the directory and (where helpful) the leading NN_
// from a filename so the TUI list reads cleanly.
func displayFor(rel string) string {
	base := filepath.Base(rel)
	base = strings.TrimSuffix(base, ".md")
	// Strip leading "NN_" if present.
	rx := regexp.MustCompile(`^\d{2,}_`)
	base = rx.ReplaceAllString(base, "")
	base = strings.ReplaceAll(base, "_", " ")
	return base
}

// slugify turns "Auth Flow!" into "auth_flow". Used for note filenames.
func slugify(s string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		case r == ' ' || r == '_' || r == '-':
			if !prevUnderscore && b.Len() > 0 {
				b.WriteRune('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}
