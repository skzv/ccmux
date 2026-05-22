// Package notes is the read-only operations layer for the TUI's Notes
// tab. One project's notes vault == every markdown file under the
// project root, grouped by the folder it lives in. Plain markdown on
// the filesystem is the source of truth; ccmux browses, renders, and
// searches it but does not create or template notes — writing files is
// the user's (or their agent's) job.
package notes

import (
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

// Open returns a Vault rooted at the project directory.
func Open(projectPath string) Vault {
	return Vault{Root: projectPath}
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
		// Inside an Agent Logs folder, newest-first is friendlier
		// (the filename is the date).
		if filepath.Base(out[i].Dir) == "03_Agent_Logs" {
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

// skipDir reports whether a directory should be pruned from the vault
// walk. Hidden directories (.git, .obsidian, .ccmux) plus the usual
// dependency and build-output trees hold vendored markdown that would
// bury the project's own notes.
func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist", "build", "target", "__pycache__":
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
