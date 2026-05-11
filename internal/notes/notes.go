// Package notes is the on-disk operations layer for the TUI's Notes tab.
// One project's notes vault == its docs/ tree. No global vault, no
// required sync service — plain markdown on the filesystem is the
// source of truth.
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

// Vault is the docs/ tree for one project.
type Vault struct {
	Root string // absolute path to <project>/docs
}

// Open returns a Vault rooted at <projectPath>/docs. The directory does
// not need to exist yet — New* methods create it on first write.
func Open(projectPath string) Vault {
	return Vault{Root: filepath.Join(projectPath, "docs")}
}

// Section identifies which of the three canonical subdirectories a note
// lives in. Notes elsewhere in docs/ are still listed (under "Other")
// but the quick-actions only create in these three.
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

// Entry is one item in the vault listing.
type Entry struct {
	Path     string    // absolute path on disk
	Rel      string    // path relative to Vault.Root (e.g. "01_Specs/00_Vision.md")
	Section  Section   // which canonical section this entry belongs to
	Display  string    // short, human-readable label for the TUI
	Modified time.Time // mtime, for sorting
}

// List returns every markdown file under the vault, grouped (in the
// returned slice) so SectionSpecs entries come first, then Architecture,
// Agent Logs, then Other. Within a section: 01_Specs sort lexically;
// Agent Logs sort newest-first by filename (which is the date).
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
			// Skip hidden directories (.obsidian, .git stray copies).
			if strings.HasPrefix(d.Name(), ".") && path != v.Root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(v.Root, path)
		info, _ := d.Info()
		out = append(out, Entry{
			Path:     path,
			Rel:      rel,
			Section:  sectionForRel(rel),
			Display:  displayFor(rel),
			Modified: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Section != out[j].Section {
			return out[i].Section < out[j].Section
		}
		// Inside Agent Logs, newest-first is friendlier.
		if out[i].Section == SectionAgentLogs {
			return out[i].Rel > out[j].Rel
		}
		return out[i].Rel < out[j].Rel
	})
	return out, nil
}

// Read returns the bytes of the file at `rel` (a path relative to the
// vault root). Wraps the canonical filesystem error.
func (v Vault) Read(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(v.Root, rel))
}

// NewAgentLog returns the path to today's agent log, creating it from a
// template if it doesn't exist yet. If `extraSession` is non-empty,
// appends a session-start entry to the log immediately after the header
// (used by ccmux when a new Claude session is launched for this project).
func (v Vault) NewAgentLog(extraSession string) (string, bool, error) {
	dir := filepath.Join(v.Root, SectionAgentLogs.Dir())
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

`, today, projectNameFor(v.Root), today)
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
	dir := filepath.Join(v.Root, sec.Dir())
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

// sectionForRel decides which canonical Section a vault-relative path
// belongs to based on its top-level directory.
func sectionForRel(rel string) Section {
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
	if len(parts) == 0 {
		return SectionOther
	}
	switch parts[0] {
	case "01_Specs":
		return SectionSpecs
	case "02_Architecture":
		return SectionArchitecture
	case "03_Agent_Logs":
		return SectionAgentLogs
	}
	return SectionOther
}

// displayFor strips the section prefix and (where helpful) the leading
// NN_ from a filename so the TUI list reads cleanly.
func displayFor(rel string) string {
	base := filepath.Base(rel)
	base = strings.TrimSuffix(base, ".md")
	// Strip leading "NN_" if present.
	rx := regexp.MustCompile(`^\d{2,}_`)
	base = rx.ReplaceAllString(base, "")
	base = strings.ReplaceAll(base, "_", " ")
	return base
}

// projectNameFor extracts the project basename from a vault root path
// (which always ends in "/docs").
func projectNameFor(vaultRoot string) string {
	return filepath.Base(filepath.Dir(vaultRoot))
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
