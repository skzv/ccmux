// Package muse reads Meta Muse Code's durable local session logs. It never
// loads credentials, encrypted reasoning, or the derived MSP cache as history.
package muse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func xdg(home, key, fallback string) string {
	if value := os.Getenv(key); filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(home, fallback)
}
func ConfigRoot(home string) string {
	return filepath.Join(xdg(home, "XDG_CONFIG_HOME", ".config"), "muse")
}
func DataRoot(home string) string {
	return filepath.Join(xdg(home, "XDG_DATA_HOME", ".local/share"), "muse")
}
func SessionsRoot(home string) string { return filepath.Join(DataRoot(home), "sessions") }

type Message struct {
	Role, Content string
	Time          time.Time
}
type Request struct {
	Time                             time.Time
	Input, Output, Cached, Reasoning int
}
type Session struct {
	ID, Path, Workspace, Title string
	Paths                      []string
	LastActivity               time.Time
	Messages                   []Message
	Requests                   []Request
}

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// SessionDir validates the native dated UUID layout, never an arbitrary path
// merely ending in .jsonl. It also rejects symlinked ancestors below the root.
func SessionDir(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 5 || parts[4] != "session.jsonl" || !uuid.MatchString(parts[3]) {
		return "", fmt.Errorf("not a native Muse session path: %s", path)
	}
	if _, err := time.Parse("2006/01/02", strings.Join(parts[:3], "/")); err != nil {
		return "", fmt.Errorf("invalid Muse date: %w", err)
	}
	current := root
	for _, part := range append([]string{""}, parts...) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink in Muse session path: %s", current)
		}
	}
	return filepath.Dir(path), nil
}

// List returns one logical row per top-level session. Child logs contribute
// usage but do not turn agent-to-agent prompts into user messages.
func List(home string) ([]Session, error) {
	root := SessionsRoot(home)
	var out []Session
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if entry.IsDir() && path != root && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		if entry.Name() != "session.jsonl" || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if _, err := SessionDir(root, path); err != nil {
			return nil
		}
		s, err := Read(path)
		if err != nil {
			return nil
		}
		out = append(out, s)
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}

type record struct {
	ID       string          `json:"id"`
	Schema   int             `json:"schema_version"`
	At       int64           `json:"recorded_at"`
	Type     string          `json:"payload_type"`
	Payload  json.RawMessage `json:"payload"`
	Frame    string          `json:"retained_frame"`
	Children []struct {
		JSON string `json:"record_json"`
	} `json:"children"`
}

// Read parses the parent and its nested subagent logs with bounded line sizes.
// It ignores unknown event kinds and malformed/incomplete lines from live writes.
func Read(path string) (Session, error) {
	s := Session{ID: filepath.Base(filepath.Dir(path)), Path: path}
	if !uuid.MatchString(s.ID) {
		return s, fmt.Errorf("invalid Muse session UUID")
	}
	info, err := os.Stat(path)
	if err != nil {
		return s, err
	}
	// Prefer recorded activity over copy/restore modification times.
	paths := []string{path}
	err = filepath.WalkDir(filepath.Dir(path), func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if e.IsDir() && p != filepath.Dir(path) && strings.HasPrefix(e.Name(), ".") {
			return filepath.SkipDir
		}
		if p != path && e.Name() == "session.jsonl" {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return s, err
	}
	s.Paths = paths
	seen, messages := map[string]bool{}, map[string]bool{}
	var visit func(record, bool, int)
	visit = func(r record, parent bool, depth int) {
		if depth > 4 {
			return
		}
		if r.Frame != "" {
			for _, child := range r.Children {
				var nested record
				if json.Unmarshal([]byte(child.JSON), &nested) == nil {
					visit(nested, parent, depth+1)
				}
			}
			return
		}
		if r.Schema != 1 {
			return
		}
		var p struct {
			Kind     string `json:"kind"`
			RunID    string `json:"run_id"`
			IntentID string `json:"intent_id"`
			NewName  string `json:"new_name"`
			SourceID string `json:"source_run_record_id"`
			Record   struct {
				Workspace string `json:"workspace_root"`
			} `json:"record"`
			Event struct {
				Kind, Prompt, Text string
				MessageID          string `json:"message_id"`
				Usage              *struct {
					Input     int `json:"input_tokens"`
					Output    int `json:"output_tokens"`
					Cached    int `json:"cached_tokens"`
					Reasoning int `json:"reasoning_tokens"`
				} `json:"usage"`
			} `json:"event"`
			Blocks []struct{ Kind, Text string } `json:"refill_blocks"`
		}
		if json.Unmarshal(r.Payload, &p) != nil {
			return
		}
		key := r.ID
		if p.SourceID != "" {
			key = p.SourceID
		}
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		at := time.UnixMicro(r.At)
		if r.At > 0 && at.After(s.LastActivity) {
			s.LastActivity = at
		}
		addMessage := func(id, role, text string) {
			key := role + ":" + id
			if !parent || id == "" || strings.TrimSpace(text) == "" || messages[key] {
				return
			}
			messages[key] = true
			s.Messages = append(s.Messages, Message{role, text, at})
		}
		switch r.Type {
		case "runtime.session.metadata":
			if parent {
				s.Workspace = p.Record.Workspace
			}
		case "session.name.changed":
			if parent {
				s.Title = p.NewName
			}
		case "runtime.user_intent.accepted":
			var parts []string
			for _, b := range p.Blocks {
				if b.Kind == "text" {
					parts = append(parts, b.Text)
				}
			}
			addMessage(p.IntentID, "user", strings.Join(parts, "\n"))
		case "runtime.session":
			if p.Kind != "run" {
				return
			}
			switch p.Event.Kind {
			case "started":
				addMessage(p.RunID, "user", p.Event.Prompt)
			case "assistant_message_committed":
				addMessage(p.Event.MessageID, "assistant", p.Event.Text)
			case "model_completed":
				if u := p.Event.Usage; u != nil && u.Input >= 0 && u.Output >= 0 && u.Cached >= 0 && u.Reasoning >= 0 {
					s.Requests = append(s.Requests, Request{at, u.Input, u.Output, u.Cached, u.Reasoning})
				}
			}
		}
	}
	for _, p := range paths {
		file, err := os.Open(p)
		if err != nil {
			return s, err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			var r record
			if json.Unmarshal(scanner.Bytes(), &r) == nil {
				visit(r, p == path, 0)
			}
		}
		err = scanner.Err()
		file.Close()
		if err != nil {
			return s, fmt.Errorf("read Muse log: %w", err)
		}
	}
	if s.LastActivity.IsZero() {
		s.LastActivity = info.ModTime()
	}
	sort.SliceStable(s.Messages, func(i, j int) bool { return s.Messages[i].Time.Before(s.Messages[j].Time) })
	return s, nil
}
