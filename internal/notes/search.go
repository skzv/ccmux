package notes

import (
	"bufio"
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
)

// SearchHit is one match returned by Vault.Search.
type SearchHit struct {
	Path    string // absolute path on disk
	Rel     string // path relative to Vault.Root
	LineNum int
	Snippet string // the matching line, trimmed of leading whitespace
}

// Search runs a case-insensitive search across every markdown file
// under the vault root. Uses `rg --json` when ripgrep is on PATH —
// faster, gitignore-aware, and respects file types — and falls back
// to a pure-Go scanner otherwise so search works on every install.
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

// searchRipgrep shells out to `rg --json --type md --smart-case
// --max-count 5 -- <query> <root>` and parses the JSON-lines output.
// Smart-case means lowercase queries match case-insensitively while
// queries with any uppercase character match exactly — matches what
// most users expect from a search box. max-count caps hits per file
// so a single noisy doc doesn't drown the rest of the vault.
func (v Vault) searchRipgrep(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	cmd := exec.CommandContext(ctx, "rg",
		"--json", "--type", "md", "--smart-case",
		"--max-count", "5",
		"--", query, v.Root,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// rg exits 1 when there are no matches — that's not a real error.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("rg: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	var hits []SearchHit
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<16), 1<<22)
	for sc.Scan() && len(hits) < limit {
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
	return hits, nil
}

// searchFallback is the no-ripgrep path: walk every .md file under
// the root, scan each line for the query (case-insensitive substring
// match), build the same SearchHit list. Bounded so it stays usable
// on a vault with thousands of files.
func (v Vault) searchFallback(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	needle := strings.ToLower(query)
	var hits []SearchHit
	err := filepath.WalkDir(v.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil || len(hits) >= limit {
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
		select {
		case <-ctx.Done():
			return errors.New("cancelled")
		default:
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<16), 1<<22)
		perFile := 0
		for ln := 1; sc.Scan(); ln++ {
			if perFile >= 5 || len(hits) >= limit {
				break
			}
			line := sc.Text()
			if strings.Contains(strings.ToLower(line), needle) {
				hits = append(hits, hitFor(v.Root, path, ln, line))
				perFile++
			}
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
// the absolute + relative paths and trimming the snippet.
func hitFor(root, absPath string, line int, snippet string) SearchHit {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		rel = absPath
	}
	return SearchHit{
		Path:    absPath,
		Rel:     rel,
		LineNum: line,
		Snippet: strings.TrimSpace(strings.TrimRight(snippet, "\n")),
	}
}
