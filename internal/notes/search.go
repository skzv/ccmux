package notes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skzv/ccmux/internal/jsonl"
)

// SearchHit is one match returned by Vault.Search.
type SearchHit struct {
	Path    string // absolute path on disk
	Rel     string // path relative to Vault.Root
	LineNum int
	Snippet string // the matching line, trimmed of leading whitespace
}

// Search runs a case-insensitive literal search across every markdown file
// under the vault root — the same file set List returns. Uses
// `rg --json` when ripgrep is on PATH and falls back to a pure-Go
// scanner otherwise (or when rg fails outright) so search works on
// every install.
//
// `limit` caps the number of hits returned (0 → default 100) so a
// pathological query against a huge docs tree doesn't lock the TUI.
func (v Vault) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	if _, err := os.Stat(v.Root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if _, err := exec.LookPath("rg"); err == nil {
		return v.searchRipgrep(ctx, query, limit)
	}
	return v.searchFallback(ctx, query, limit)
}

// maxSearchLineBytes caps one line of input: a note line in the
// fallback, one rg JSON record in the ripgrep path. Longer lines are
// skipped (a single minified blob must not fail the whole search).
const maxSearchLineBytes = 4 << 20

// ripgrepArgs builds the rg invocation. The file set mirrors List:
// every *.md (any case) under the root, hidden files included, hidden
// and prunedDirs directories excluded, and no .gitignore / .ignore /
// global-ignore filtering — List shows gitignored notes, so search must
// find them. --no-config keeps a user's RIPGREP_CONFIG_PATH from
// changing any of that.
func ripgrepArgs(query, root string) []string {
	args := []string{
		"--json", "--no-config", "--no-ignore", "--hidden",
		"--iglob", "*.md", "--glob", "!.*/",
	}
	for _, d := range prunedDirs {
		args = append(args, "--glob", "!"+d+"/")
	}
	return append(args,
		"--fixed-strings", "--ignore-case", "--max-count", "5",
		"--", query, root,
	)
}

// searchRipgrep streams JSON-lines output, stopping the process once
// enough hits have arrived. max-count also caps hits per file.
func (v Vault) searchRipgrep(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(searchCtx, "rg", ripgrepArgs(query, v.Root)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("rg stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("start rg: %w", err)
	}

	var hits []SearchHit
	// jsonl.Scanner skips a record longer than the cap (a match on a
	// multi-MiB line) instead of failing the whole read the way
	// bufio.Scanner's ErrTooLong did.
	sc := jsonl.NewScanner(out, maxSearchLineBytes)
	for len(hits) < limit && sc.Scan() {
		var rec struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				Lines struct {
					Text string `json:"text"`
				} `json:"lines"`
				LineNumber int `json:"line_number"`
			} `json:"data"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Type != "match" {
			continue
		}
		hits = append(hits, hitFor(v.Root, rec.Data.Path.Text, rec.Data.LineNumber, rec.Data.Lines.Text))
	}
	scanErr := sc.Err()
	if len(hits) >= limit || scanErr != nil {
		cancel()
		_ = out.Close()
	}
	waitErr := cmd.Wait() // Always reap, including early limit/error exits.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if scanErr != nil {
		return nil, fmt.Errorf("read rg output: %w", scanErr)
	}
	if len(hits) >= limit {
		return hits, nil
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			switch exitErr.ExitCode() {
			case 1: // no matches — not an error
				return nil, nil
			case 2:
				// rg exits 2 when any path errored (an unreadable
				// subdirectory, a file vanishing mid-walk) even though
				// it searched — and matched — everything else. Those
				// hits are real; keep them. With none, rg may have
				// failed outright, so let the fallback answer.
				if len(hits) > 0 {
					return hits, nil
				}
				return v.searchFallback(ctx, query, limit)
			}
		}
		return nil, fmt.Errorf("rg: %w (%s)", waitErr, strings.TrimSpace(stderr.String()))
	}
	return hits, nil
}

// searchFallback is the no-ripgrep path: walk every .md file under
// the root, scan each line for the query (case-insensitive substring
// match), build the same SearchHit list. Bounded so it stays usable
// on a vault with thousands of files. Unreadable directories and files
// are skipped, as are lines over maxSearchLineBytes — one bad file
// never fails the search.
func (v Vault) searchFallback(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	needle := strings.ToLower(query)
	var hits []SearchHit
	err := filepath.WalkDir(v.Root, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(hits) >= limit {
			return filepath.SkipAll
		}
		if err != nil {
			return nil
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
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := jsonl.NewScanner(f, maxSearchLineBytes)
		perFile, read := 0, 0
		for perFile < 5 && len(hits) < limit {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !sc.Scan() {
				break
			}
			read++
			line := sc.Text()
			if strings.Contains(strings.ToLower(line), needle) {
				// Skipped (oversized) lines still occupy a line number.
				hits = append(hits, hitFor(v.Root, path, read+sc.Skipped(), line))
				perFile++
			}
		}
		if len(hits) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Sort fallback hits by rel + line so the TUI list reads
	// consistently across the rg and non-rg paths.
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Rel != hits[j].Rel {
			return hits[i].Rel < hits[j].Rel
		}
		return hits[i].LineNum < hits[j].LineNum
	})
	return hits, nil
}

// hitFor builds a SearchHit from raw rg/fallback fields, normalizing
// the absolute + relative paths and trimming the snippet. Rel is
// slash-separated (filepath.ToSlash) so hits match Entry.Rel's
// convention on Windows too.
func hitFor(root, absPath string, line int, snippet string) SearchHit {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		rel = absPath
	}
	rel = filepath.ToSlash(rel)
	return SearchHit{
		Path:    absPath,
		Rel:     rel,
		LineNum: line,
		Snippet: strings.TrimSpace(strings.TrimRight(snippet, "\n")),
	}
}
