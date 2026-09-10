// Package gemini reads Gemini CLI's native history without changing its
// settings or credentials. Formats follow google-gemini/gemini-cli's
// chatRecordingTypes.ts, chatRecordingService.ts and projectRegistry.ts.
package gemini

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func ConfigRoot(home string) string   { return filepath.Join(home, ".gemini") }
func SessionsRoot(home string) string { return filepath.Join(ConfigRoot(home), "tmp") }

type Tokens struct {
	Input    int `json:"input"`
	Output   int `json:"output"`
	Cached   int `json:"cached"`
	Thoughts int `json:"thoughts"`
}

type Message struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	Timestamp string          `json:"timestamp"`
	Tokens    *Tokens         `json:"tokens"`
}

// Text handles both legacy string content and native multimodal parts. Tool
// payloads and inline binary data are intentionally excluded from previews.
func (m Message) Text() string {
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		return text
	}
	var parts []json.RawMessage
	if json.Unmarshal(m.Content, &parts) != nil {
		parts = []json.RawMessage{m.Content}
	}
	var out strings.Builder
	for _, raw := range parts {
		var part struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &text) == nil {
			out.WriteString(text)
		} else if json.Unmarshal(raw, &part) == nil {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}

type Session struct {
	ID          string    `json:"sessionId"`
	ProjectHash string    `json:"projectHash"`
	StartTime   string    `json:"startTime"`
	LastUpdated string    `json:"lastUpdated"`
	Messages    []Message `json:"messages"`
	Kind        string    `json:"kind"`
	Path        string    `json:"-"`
	Paths       []string  `json:"-"`
	Project     string    `json:"-"`
	Updated     time.Time `json:"-"`
}

// Read supports legacy JSON and the append-only JSONL format. Repeated message
// IDs replace earlier records; rewinds/checkpoints remove superseded messages.
func Read(path string) (Session, error) {
	s := Session{Path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if strings.HasSuffix(path, ".json") {
		err = json.Unmarshal(b, &s)
	} else {
		scanner := bufio.NewScanner(bytes.NewReader(b))
		scanner.Buffer(make([]byte, 4096), 16*1024*1024)
		for scanner.Scan() {
			var record map[string]json.RawMessage
			if json.Unmarshal(scanner.Bytes(), &record) != nil {
				continue // live recordings may end with a partial line
			}
			if raw, ok := record["$rewindTo"]; ok {
				var id string
				if json.Unmarshal(raw, &id) != nil {
					continue
				}
				end := 0
				for i, m := range s.Messages {
					if m.ID == id {
						end = i
						break
					}
				}
				s.Messages = s.Messages[:end]
			} else if raw, ok := record["$set"]; ok {
				// Unmarshal into the existing struct preserves fields absent
				// from a metadata update, including the current messages.
				if e := json.Unmarshal(raw, &s); e != nil {
					return s, e
				}
			} else if _, ok := record["id"]; ok {
				var m Message
				if json.Unmarshal(scanner.Bytes(), &m) != nil || m.ID == "" {
					continue
				}
				found := false
				for i := range s.Messages {
					if s.Messages[i].ID == m.ID {
						s.Messages[i] = m
						found = true
						break
					}
				}
				if !found {
					s.Messages = append(s.Messages, m)
				}
			} else if _, ok := record["sessionId"]; ok {
				if e := json.Unmarshal(scanner.Bytes(), &s); e != nil {
					return s, e
				}
			}
		}
		err = scanner.Err()
	}
	if err != nil {
		return s, err
	}
	if s.ID == "" || s.ProjectHash == "" {
		return s, fmt.Errorf("invalid Gemini session metadata in %s", path)
	}
	s.Updated, _ = time.Parse(time.RFC3339Nano, s.LastUpdated)
	for _, m := range s.Messages {
		if ts, e := time.Parse(time.RFC3339Nano, m.Timestamp); e == nil && ts.After(s.Updated) {
			s.Updated = ts
		}
	}
	if s.Updated.IsZero() {
		if info, e := os.Stat(path); e == nil {
			s.Updated = info.ModTime()
		}
	}
	return s, nil
}

func IsSessionPath(home, path string) bool {
	rel, err := filepath.Rel(SessionsRoot(home), path)
	if err != nil {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return len(parts) == 3 && parts[0] != ".." && parts[1] == "chats" &&
		(strings.HasSuffix(parts[2], ".json") || strings.HasSuffix(parts[2], ".jsonl"))
}

// List reads only the Gemini chats directory, excluding Antigravity's nested
// tree. JSONL takes precedence over a legacy JSON copy of the same session.
func List(home string) ([]Session, error) {
	projects := projectPaths(home)
	byID := map[string]Session{}
	err := filepath.WalkDir(SessionsRoot(home), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 || !IsSessionPath(home, path) {
			return nil
		}
		s, err := Read(path)
		if err != nil {
			return nil
		}
		projectDir := filepath.Dir(filepath.Dir(path))
		if b, e := os.ReadFile(filepath.Join(projectDir, ".project_root")); e == nil {
			if root := strings.TrimSpace(string(b)); filepath.IsAbs(root) {
				s.Project = filepath.Clean(root)
			}
		}
		if s.Project == "" {
			s.Project = projects[filepath.Base(projectDir)]
		}
		if s.Project == "" {
			s.Project = projects[s.ProjectHash]
		}
		key := s.ProjectHash + "\x00" + s.ID
		old, exists := byID[key]
		newJSONL, oldJSONL := strings.HasSuffix(s.Path, ".jsonl"), strings.HasSuffix(old.Path, ".jsonl")
		paths := append(old.Paths, s.Path)
		if !exists || (newJSONL && !oldJSONL) || (newJSONL == oldJSONL && s.Updated.After(old.Updated)) {
			old = s
		}
		old.Paths = paths
		byID[key] = old
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(byID))
	for _, s := range byID {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func projectPaths(home string) map[string]string {
	var registry struct {
		Projects map[string]string `json:"projects"`
	}
	if b, err := os.ReadFile(filepath.Join(ConfigRoot(home), "projects.json")); err == nil {
		_ = json.Unmarshal(b, &registry)
	}
	out := map[string]string{}
	for path, slug := range registry.Projects {
		if !filepath.IsAbs(path) {
			continue
		}
		out[slug] = filepath.Clean(path)
		out[fmt.Sprintf("%x", sha256.Sum256([]byte(path)))] = filepath.Clean(path)
	}
	return out
}

// ValidateDelete verifies ownership and resolves symlinks before a caller
// deletes a transcript. A settings file or another agent's history is rejected.
func ValidateDelete(home, id, path string) error {
	if !IsSessionPath(home, path) {
		return fmt.Errorf("not a Gemini chat transcript: %s", path)
	}
	root, err := filepath.EvalSymlinks(SessionsRoot(home))
	if err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("Gemini transcript escapes its root: %s", path)
	}
	s, err := Read(path)
	if err != nil {
		return err
	}
	if s.ID != id {
		return fmt.Errorf("Gemini session ID does not match transcript")
	}
	return nil
}
