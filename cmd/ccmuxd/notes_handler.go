package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
)

const notesSearchTimeout = 10 * time.Second

// handleNotes serves both list and read for a project's markdown
// vault — `?project=<name>` returns the list, `&file=<rel>` returns
// the contents of one file.
//
// Security: project is resolved against the daemon's configured
// Projects.Root via project.Discover (same source as the dashboard),
// so a caller can only ever reference projects ccmux already lists.
// The file query is path-traversal-validated below — clients can't
// reach outside the project root via "../" or absolute paths.
func (s *server) handleNotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	proj, ok := s.resolveProject(w, r)
	if !ok {
		return
	}
	vault := notes.Open(proj.Path)

	if rel := strings.TrimSpace(r.URL.Query().Get("file")); rel != "" {
		// Path-traversal hardening: notes.CleanRel rejects absolute
		// paths and ".." escapes (Vault.Read re-checks); only .md files
		// are served.
		cleaned, err := notes.CleanRel(rel)
		if err != nil {
			http.Error(w, "file must be a project-relative path: "+err.Error(), http.StatusBadRequest)
			return
		}
		if !strings.HasSuffix(strings.ToLower(cleaned), ".md") {
			http.Error(w, "only .md files are served", http.StatusBadRequest)
			return
		}
		body, err := vault.Read(cleaned)
		if err != nil {
			switch {
			case errors.Is(err, notes.ErrOutsideVault):
				http.Error(w, "file must stay inside project", http.StatusBadRequest)
			case errors.Is(err, notes.ErrNoteTooLarge):
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			case os.IsNotExist(err):
				http.Error(w, "file not found", http.StatusNotFound)
			default:
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		writeJSON(w, daemon.NoteContent{Rel: cleaned, Content: string(body)})
		return
	}

	entries, err := vault.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]daemon.NoteEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, daemon.NoteEntry{
			Rel:      e.Rel,
			Dir:      e.Dir,
			Display:  e.Display,
			Modified: e.Modified,
		})
	}
	writeJSON(w, out)
}

// resolveProject reads the required ?project=<name> query, resolves it
// against the daemon's configured Projects.Root via project.Discover
// (same source as handleNotes/the dashboard, so a caller can only ever
// reference projects ccmux already lists), and writes the appropriate
// HTTP error if the name is missing or unknown. The bool reports whether
// the caller should continue.
func (s *server) resolveProject(w http.ResponseWriter, r *http.Request) (project.Project, bool) {
	name := strings.TrimSpace(r.URL.Query().Get("project"))
	if name == "" {
		http.Error(w, "project query required", http.StatusBadRequest)
		return project.Project{}, false
	}
	projs, err := project.Discover(project.ResolveRoot(s.cfg.Projects.Root))
	if err != nil {
		http.Error(w, "discover: "+err.Error(), http.StatusInternalServerError)
		return project.Project{}, false
	}
	for _, p := range projs {
		if p.Name == name {
			return p, true
		}
	}
	http.Error(w, "project not found", http.StatusNotFound)
	return project.Project{}, false
}

// handleNotesSearch serves GET /v1/notes/search?project=<name>&q=<query>,
// returning search hits from the project's markdown vault. The project is
// validated the same way as handleNotes; the query itself never becomes a
// filesystem path, so there's no traversal surface to harden here.
func (s *server) handleNotesSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	proj, ok := s.resolveProject(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		http.Error(w, "q query required", http.StatusBadRequest)
		return
	}
	searchCtx, cancel := context.WithTimeout(r.Context(), notesSearchTimeout)
	defer cancel()
	hits, err := notes.Open(proj.Path).Search(searchCtx, query, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]daemon.SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, daemon.SearchHit{
			Rel:     h.Rel,
			LineNum: h.LineNum,
			Snippet: h.Snippet,
		})
	}
	writeJSON(w, out)
}
