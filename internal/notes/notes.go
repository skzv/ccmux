// Package notes is the read-only operations layer for the TUI's Notes
// tab. One project's notes vault == every markdown file under the
// project root, grouped by the folder it lives in. Plain markdown on
// the filesystem is the source of truth; ccmux browses, renders, and
// searches it but does not create or template notes — writing files is
// the user's (or their agent's) job.
package notes

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
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
			Display:  displayFor(rel, path, mod),
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

// displayFor returns the row label for a note: the leading H1 text
// when the file has one within the first 4 KiB, otherwise the
// cleaned-up filename. The H1 lookup is memoized per `(absPath,
// mtime)` so the list doesn't re-read every file on every render.
// `absPath` and `mod` may be zero for callers that only have a
// relative path; in that case the filename fallback is used.
func displayFor(rel, absPath string, mod time.Time) string {
	if absPath != "" {
		if h1 := cachedH1(absPath, mod); h1 != "" {
			return h1
		}
	}
	return filenameLabel(rel)
}

// filenameLabel returns the cleaned-up filename label: strip the .md
// suffix, drop the leading "NN_" sort prefix when present, and turn
// underscores into spaces.
func filenameLabel(rel string) string {
	base := filepath.Base(rel)
	base = strings.TrimSuffix(base, ".md")
	rx := regexp.MustCompile(`^\d{2,}_`)
	base = rx.ReplaceAllString(base, "")
	base = strings.ReplaceAll(base, "_", " ")
	return base
}

// h1ScanBytes caps how much of a file we read looking for the leading
// H1. 4 KiB covers any realistic frontmatter block + first heading
// without pulling whole long notes into memory at list time.
const h1ScanBytes = 4 * 1024

// h1HeadingRE matches an ATX-style H1: a line starting with exactly
// one '#', a space, then heading text. Optional trailing '#' closers
// (per CommonMark) are stripped by the caller.
var h1HeadingRE = regexp.MustCompile(`^#\s+(.+?)\s*#*\s*$`)

// h1CacheEntry is one row in the H1 memo: the discovered heading
// text (empty when none was found in the scan window) keyed by file
// mtime, so a write to the same path bypasses the cache.
type h1CacheEntry struct {
	mtime time.Time
	h1    string
}

var (
	h1CacheMu sync.RWMutex
	h1Cache   = map[string]h1CacheEntry{}
)

// cachedH1 returns the leading H1 text for `absPath` at `mtime`,
// reading and memoizing on miss. An empty return means the file
// has no H1 in its first h1ScanBytes (or couldn't be opened).
func cachedH1(absPath string, mtime time.Time) string {
	h1CacheMu.RLock()
	hit, ok := h1Cache[absPath]
	h1CacheMu.RUnlock()
	if ok && hit.mtime.Equal(mtime) {
		return hit.h1
	}
	h1 := extractH1(absPath)
	h1CacheMu.Lock()
	h1Cache[absPath] = h1CacheEntry{mtime: mtime, h1: h1}
	h1CacheMu.Unlock()
	return h1
}

// extractH1 reads up to h1ScanBytes from `path` and returns the first
// ATX-style H1 heading line's text, or "" when none is present in
// that window. YAML/TOML frontmatter (delimited by `---` or `+++` at
// the top of the file) is skipped over so an H1 buried under
// frontmatter still surfaces. The reader is bounded so a binary
// (e.g. someone named a .md after a generated artifact) can't
// blow memory.
func extractH1(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(io.LimitReader(f, h1ScanBytes))
	scanner.Buffer(make([]byte, 0, 4096), h1ScanBytes)

	inFrontmatter := false
	frontDelim := ""
	firstLine := true
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimRight(line, "\r")
		if firstLine {
			firstLine = false
			if trimmed == "---" {
				inFrontmatter = true
				frontDelim = "---"
				continue
			}
			if trimmed == "+++" {
				inFrontmatter = true
				frontDelim = "+++"
				continue
			}
		}
		if inFrontmatter {
			if trimmed == frontDelim {
				inFrontmatter = false
			}
			continue
		}
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		if m := h1HeadingRE.FindStringSubmatch(trimmed); m != nil {
			return strings.TrimSpace(m[1])
		}
		// First non-empty, non-frontmatter line that isn't an H1 —
		// stop looking; this isn't a "title at top" doc.
		return ""
	}
	return ""
}
