package main

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
)

// The Telegram notes web viewer. Optional (config [telegram].web_viewer)
// and tailnet-only by construction: it binds to the host's 100.x
// tailnet address, so it is unreachable from the public internet — we
// never use `tailscale funnel`. It serves a project's markdown vault so
// the Telegram in-app browser (which renders .md natively) can browse
// the whole vault with working links, beyond the single-file
// sendDocument path.

// startWebViewer binds the viewer to the tailnet address when enabled
// and returns its base URL (or "" when disabled / no tailnet). Safe to
// call unconditionally.
func (s *server) startWebViewer(ctx context.Context) string {
	if !s.cfg.Telegram.WebViewer {
		return ""
	}
	port := s.cfg.Daemon.TailnetPort + 1
	if port <= 1 {
		port = 7475
	}
	addr, err := tailscaleAddr(ctx, port)
	if err != nil {
		log.Printf("ccmuxd: telegram web viewer disabled (no tailnet address): %v", err)
		return ""
	}
	srv := newHTTPServer(s.webViewerHandler())
	srv.Addr = addr
	go func() {
		log.Printf("ccmuxd: telegram web viewer on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("ccmuxd: web viewer serve: %v", err)
		}
	}()
	return "http://" + addr
}

// webViewerHandler serves GET /notes/<project>[/<rel>]. The list page is
// minimal HTML linking each file; a file is served as raw markdown
// (Content-Type text/markdown) which the Telegram in-app browser renders
// formatted. Access is confined to discovered project vaults and
// traversal-checked.
func (s *server) webViewerHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleViewer)
	return mux
}

func (s *server) handleViewer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/notes/")
	if r.URL.Path == "/" || r.URL.Path == "/notes" || r.URL.Path == "/notes/" || rest == "" {
		s.viewerProjectIndex(w)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	proj, ok := s.findProject(parts[0])
	if !ok {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	vault := notes.Open(proj.Path)
	if len(parts) == 1 {
		s.viewerVaultIndex(w, proj.Name, vault)
		return
	}
	rel := parts[1]
	if !viewerSafeRel(rel) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	body, err := vault.Read(filepath.ToSlash(filepath.Clean(rel)))
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write(body)
}

func (s *server) findProject(name string) (project.Project, bool) {
	projs, err := project.Discover(s.cfg.Projects.Root)
	if err != nil {
		return project.Project{}, false
	}
	for _, p := range projs {
		if p.Name == name {
			return p, true
		}
	}
	return project.Project{}, false
}

func (s *server) viewerProjectIndex(w http.ResponseWriter) {
	projs, _ := project.Discover(s.cfg.Projects.Root)
	var b strings.Builder
	b.WriteString("<!doctype html><meta charset=utf-8><title>ccmux notes</title><h1>Projects</h1><ul>")
	for _, p := range projs {
		fmt.Fprintf(&b, `<li><a href="/notes/%s">%s</a></li>`, html.EscapeString(p.Name), html.EscapeString(p.Name))
	}
	b.WriteString("</ul>")
	writeHTML(w, b.String())
}

func (s *server) viewerVaultIndex(w http.ResponseWriter, project string, vault notes.Vault) {
	entries, _ := vault.List()
	var b strings.Builder
	fmt.Fprintf(&b, "<!doctype html><meta charset=utf-8><title>%s notes</title><h1>%s</h1><ul>",
		html.EscapeString(project), html.EscapeString(project))
	for _, e := range entries {
		label := e.Display
		if label == "" {
			label = e.Rel
		}
		fmt.Fprintf(&b, `<li><a href="/notes/%s/%s">%s</a></li>`,
			html.EscapeString(project), html.EscapeString(e.Rel), html.EscapeString(label))
	}
	b.WriteString("</ul>")
	writeHTML(w, b.String())
}

func writeHTML(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(s))
}

// viewerSafeRel mirrors handleNotes' path hardening: project-relative,
// no traversal, .md only.
func viewerSafeRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return false
	}
	return strings.HasSuffix(strings.ToLower(cleaned), ".md")
}
