// Package project discovers and represents projects on disk.
// A "project" is any directory containing either a CLAUDE.md or a .git directory
// under the configured projects root (~/Projects by default).
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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
	Modified time.Time // most-recent mtime among CLAUDE.md / README.md / docs/
}

// SessionName returns the ccmux tmux session name for this project,
// matching the existing `c-<basename>` convention.
func (p Project) SessionName() string {
	return "c-" + strings.ReplaceAll(p.Name, ".", "_")
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
	if !p.HasGit && !p.HasCM {
		return Project{}, false
	}
	return p, true
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
