// Package project discovers and represents projects on disk.
// A "project" is any directory under the configured projects root
// (~/Projects by default) containing one of:
//   - a .git directory
//   - a CLAUDE.md file
//   - a .ccmux/ directory (the explicit adoption marker — see Adopt)
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/agent"
)

// agentSidecarRelPath is where each project's chosen agent is stored.
// Lives under <project>/.ccmux/ rather than at the project root so we
// can add more per-project state files later (per-project secrets,
// last-attached timestamps, etc.) without crowding the repo.
const agentSidecarRelPath = ".ccmux/agent"

// Project is one bookmark-able working directory.
type Project struct {
	Name string // basename of the directory
	// Host marks which machine this project lives on. Empty / "local"
	// for the current device; otherwise the short Tailscale name of
	// the remote ccmuxd we fetched it from. The TUI uses this to
	// route attach actions (local: scaffold + tmux locally; remote:
	// POST /v1/sessions to the peer and ssh-attach).
	Host     string
	Path     string    // absolute path on the project's host
	HasGit   bool      // .git exists
	HasCM    bool      // CLAUDE.md exists
	HasDocs  bool      // docs/ exists (the notes vault)
	Adopted  bool      // .ccmux/ exists — directory was explicitly registered with ccmux
	Modified time.Time // most-recent mtime among CLAUDE.md / README.md / docs/

	// Agent is the AI agent this project runs (claude, codex, antigravity).
	// Sourced from <project>/.ccmux/agent on Discover; missing file or
	// unrecognized content defaults to agent.IDClaude (the back-compat
	// path for every project scaffolded before the sidecar existed).
	Agent agent.ID
}

// SessionName returns the ccmux tmux session name for this project.
// Stays in lock-step with tmux.SessionNameForPath so the two paths
// (project-list "session name" column + scaffold's tmux.New call)
// can never disagree about a project's session name.
func (p Project) SessionName() string {
	return "c-" + sanitizeForSessionName(p.Name)
}

// sanitizeForSessionName mirrors internal/tmux.sanitizeSessionName.
// Duplicated rather than imported to avoid a project→tmux dep cycle.
// The two implementations are pinned to the same output by
// TestSessionName_MatchesTmuxSanitizer (cross-package check).
func sanitizeForSessionName(name string) string {
	if name == "" {
		return ""
	}
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		b := name[i]
		switch {
		case b >= 'a' && b <= 'z',
			b >= 'A' && b <= 'Z',
			b >= '0' && b <= '9',
			b == '_', b == '-':
			out = append(out, b)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// Discover walks `root` one level deep and returns every directory that
// looks like a project. Hidden directories are skipped.
func Discover(root string) ([]Project, error) {
	root, err := expandHome(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read projects dir %q: %w", root, err)
	}
	out := make([]Project, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(root, e.Name())
		proj, ok := inspect(p)
		if ok {
			out = append(out, proj)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Modified.After(out[j].Modified)
	})
	return out, nil
}

// Lookup returns the project for the given path, or false if the path isn't
// a recognizable project.
func Lookup(path string) (Project, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Project{}, false
	}
	return inspect(abs)
}

func inspect(path string) (Project, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return Project{}, false
	}
	p := Project{
		Name:     filepath.Base(path),
		Path:     path,
		Modified: info.ModTime(),
	}
	if fi, err := os.Stat(filepath.Join(path, ".git")); err == nil && fi.IsDir() {
		p.HasGit = true
	}
	if fi, err := os.Stat(filepath.Join(path, "CLAUDE.md")); err == nil && !fi.IsDir() {
		p.HasCM = true
		if fi.ModTime().After(p.Modified) {
			p.Modified = fi.ModTime()
		}
	}
	if fi, err := os.Stat(filepath.Join(path, "docs")); err == nil && fi.IsDir() {
		p.HasDocs = true
	}
	if fi, err := os.Stat(filepath.Join(path, ".ccmux")); err == nil && fi.IsDir() {
		p.Adopted = true
	}
	if !p.HasGit && !p.HasCM && !p.Adopted {
		return Project{}, false
	}
	p.Agent = ReadAgent(path)
	return p, true
}

// Adopt registers `path` with ccmux by creating its `.ccmux/` directory,
// so that the next Discover call surfaces the directory as a project
// even when it has no .git and no CLAUDE.md. Used by the TUI Projects
// screen's "A" key and the `ccmux project add` CLI for directories that
// the user wants to manage with ccmux but that lack the usual markers.
//
// Idempotent — adopting an already-adopted directory is a no-op and
// returns nil. The directory itself must already exist; Adopt does not
// create it (callers reach this through orphan discovery which only
// surfaces existing directories).
func Adopt(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("project: resolve %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("project: stat %q: %w", abs, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project: %q is not a directory", abs)
	}
	if err := os.MkdirAll(filepath.Join(abs, ".ccmux"), 0o755); err != nil {
		return fmt.Errorf("project: create .ccmux in %q: %w", abs, err)
	}
	return nil
}

// DiscoverOrphans walks `root` one level deep and returns every
// non-hidden subdirectory that is NOT currently recognized as a project
// (no .git, no CLAUDE.md, no .ccmux/). These are the candidates for
// Adopt — directories the user has under their projects root but that
// ccmux's Discover would skip.
//
// Returned paths are absolute and sorted by basename for stable
// rendering in the TUI picker.
func DiscoverOrphans(root string) ([]string, error) {
	root, err := expandHome(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read projects dir %q: %w", root, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(root, e.Name())
		if _, ok := inspect(p); ok {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// ReadAgent returns the agent ID recorded in `projectPath/.ccmux/agent`.
// Missing file, read error, or unrecognized content all resolve to
// agent.IDClaude — the explicit back-compat default for everything
// scaffolded before the sidecar.
//
// Exported so the daemon's per-session classifier dispatch can read
// the sidecar without going through the full project discovery path.
func ReadAgent(projectPath string) agent.ID {
	body, err := os.ReadFile(filepath.Join(projectPath, agentSidecarRelPath))
	if err != nil {
		return agent.IDClaude
	}
	if id, ok := agent.ParseID(string(body)); ok {
		return id
	}
	return agent.IDClaude
}

// SetAgent writes the project's agent choice to its sidecar. Creates
// `.ccmux/` if missing. Validates `id` via agent.ParseID so a typo'd
// caller doesn't persist garbage that ReadAgent would then drop.
//
// Used by:
//
//   - internal/scaffold on new-project to record the user's pick.
//   - The TUI Projects screen's "a" key to switch an existing
//     project's agent.
//   - The daemon's POST /v1/projects path when the client specifies
//     an agent in NewProjectRequest.
func SetAgent(projectPath string, id agent.ID) error {
	if _, ok := agent.ParseID(string(id)); !ok {
		return fmt.Errorf("project: unknown agent id %q", id)
	}
	dir := filepath.Join(projectPath, ".ccmux")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("project: create %s: %w", dir, err)
	}
	path := filepath.Join(projectPath, agentSidecarRelPath)
	// Trailing newline keeps git-add diffs clean — POSIX text files
	// should end in \n.
	return os.WriteFile(path, []byte(string(id)+"\n"), 0o644)
}

func expandHome(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}
