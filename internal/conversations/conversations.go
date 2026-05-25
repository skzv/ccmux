// Package conversations enumerates past agent conversations on disk —
// every Claude / Codex / Antigravity session the user has had,
// regardless of whether ccmux launched it. It's the read side of the
// "resume an old conversation" flow: ccmux's TUI gets a unified
// timeline, ccmux's CLI exposes `ccmux list-conversations`, and the
// resume action launches the right agent with the right CLI flag.
//
// Each agent stores transcripts differently:
//
//	Claude:       ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl
//	Codex:        ~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-<ts>-<uuid>.jsonl
//	Antigravity:  ~/.gemini/antigravity-cli/conversations/<uuid>.pb
//
// Claude and Codex use JSONL; we parse the first user event to extract
// a short preview. Antigravity uses an opaque protobuf — we can read
// the UUID from the filename and the timestamp from mtime, but the
// first-prompt preview is unavailable without a schema. The
// Conversation.Preview field stays empty for Antigravity rows.
//
// Resume contracts (the argv each agent expects):
//
//	Claude:       claude --resume <uuid>
//	Codex:        codex resume <uuid>
//	Antigravity:  agy --conversation <uuid>
//
// All three accept the conversation by ID, so a user picking a row in
// ccmux's UI gets handed straight to the right resume invocation
// without needing to know any of this.
package conversations

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/agent"
)

// Conversation is one past agent session as found on disk. Stable
// shape across supported agents so the TUI / CLI can render a uniform
// list without per-agent special-casing in the view layer.
type Conversation struct {
	// ID is the agent's own conversation/session UUID. Stable across
	// reads — that's what every agent's --resume flag accepts.
	ID string

	// Agent is the agent that owns this conversation. Drives the
	// resume-flag selection in ResumeArgs.
	Agent agent.ID

	// Project is a human-readable label for the cwd this conversation
	// ran in. Derived from Claude's encoded-path directory or
	// Codex/Antigravity transcript content; empty when unrecoverable.
	Project string

	// LastActivity is the most recent timestamp ccmux could derive.
	// Sources, in priority: last event in the transcript > file mtime.
	// Drives the default sort order (most-recent first).
	LastActivity time.Time

	// Preview is the first ~100 chars of the conversation's first
	// user prompt. Empty for Antigravity (opaque protobuf — see
	// package doc). Trimmed and single-line for display.
	Preview string

	// Path is the absolute filesystem path to the transcript file.
	// Useful for "open in editor" actions and diagnostic output.
	Path string

	// Entrypoint is the agent's own tag for how the session was
	// launched. Per-agent semantics — IsHeadless interprets the value
	// in the context of c.Agent:
	//
	//   Claude (sourced from the `entrypoint` field on the first user
	//   event):
	//     "cli"        — interactive `claude` session.
	//     "sdk-cli"    — headless / SDK (`claude -p`, the SDK,
	//                    automation wrappers).
	//
	//   Codex (sourced from `payload.originator` on the first
	//   `session_meta` event):
	//     "codex-tui"  — interactive `codex` session.
	//     "codex_exec" — headless `codex exec` run.
	//
	//   Antigravity:
	//     ""           — always. Transcripts are opaque protobuf so
	//                    we can't read a launch-mode tag.
	//
	//   "" on Claude/Codex means the parser didn't find the tag
	//   (e.g. a metadata-only stub, an older transcript format).
	//
	// Drives the default "hide automation noise" filter on the
	// conversations list. See Options.ExcludeHeadless and IsHeadless.
	Entrypoint string
}

// IsHeadless reports whether this conversation was a headless agent
// invocation (`claude -p`, the SDK, `codex exec`, …) rather than an
// interactive terminal session. The mapping is per-agent because each
// agent uses a different tag:
//
//   - Claude    → entrypoint == "sdk-cli"
//   - Codex     → originator == "codex_exec"
//   - Antigravity → never (opaque .pb transcripts carry no signal)
//
// Adding a new headless mode to an existing agent only needs an extra
// case here — every TUI/CLI surface routes through this predicate.
func (c Conversation) IsHeadless() bool {
	switch c.Agent {
	case agent.IDClaude:
		return c.Entrypoint == "sdk-cli"
	case agent.IDCodex:
		return c.Entrypoint == "codex_exec"
	}
	return false
}

// ResumeArgs returns the argv vector to launch the agent that owns
// this conversation, resuming THIS specific conversation. Caller wraps
// it in a tmux new-session or exec.Command as appropriate.
//
// Lives on the struct so the TUI's keybind handler doesn't have to
// know each agent's flag dialect — it just passes the picked
// Conversation through.
func (c Conversation) ResumeArgs() []string {
	return c.ResumeArgsWithCommands(agent.Commands{})
}

// ResumeArgsWithCommands is ResumeArgs with configured executable path
// substitution. This keeps the flag dialect owned here while allowing
// ccmux's setup-time command choice to propagate to resume flows.
func (c Conversation) ResumeArgsWithCommands(commands agent.Commands) []string {
	switch c.Agent {
	case agent.IDClaude:
		return agent.ResumeArgs(agent.IDClaude, c.ID, commands)
	case agent.IDCodex:
		return agent.ResumeArgs(agent.IDCodex, c.ID, commands)
	case agent.IDAntigravity:
		return agent.ResumeArgs(agent.IDAntigravity, c.ID, commands)
	case agent.IDCursor:
		return agent.ResumeArgs(agent.IDCursor, c.ID, commands)
	}
	// Unknown agent — empty argv; caller should treat as "can't
	// resume" rather than spawn something bogus.
	return nil
}

// Delete removes a conversation's transcript file from disk. This is
// irreversible: the transcript is gone, and with it the ability to
// resume that conversation. Callers must confirm with the user first
// — the TUI arms an x-then-x confirm, the CLI requires an explicit
// id argument.
//
// Safety guard: the path must sit under the known transcript root for
// its agent AND carry that agent's expected extension. A Conversation
// always comes from our own walkers so this is belt-and-suspenders,
// but it guarantees a hand-constructed or corrupted Conversation
// can't be turned into an arbitrary `rm` of any file on disk.
func Delete(c Conversation) error {
	if c.Path == "" {
		return fmt.Errorf("conversation %q has no transcript path", c.ID)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	if err := guardTranscriptPath(home, c); err != nil {
		return err
	}
	if err := os.Remove(c.Path); err != nil {
		return fmt.Errorf("delete transcript: %w", err)
	}
	return nil
}

// guardTranscriptPath returns nil only when c.Path is a plausible
// transcript file for c.Agent: under the agent's root directory and
// carrying the agent's file extension. Returns a descriptive error
// otherwise. filepath.Clean resolves any `..` so a path can't escape
// the root via traversal.
func guardTranscriptPath(home string, c Conversation) error {
	var root, ext string
	switch c.Agent {
	case agent.IDClaude:
		root, ext = filepath.Join(home, ".claude", "projects"), ".jsonl"
	case agent.IDCodex:
		root, ext = filepath.Join(home, ".codex", "sessions"), ".jsonl"
	case agent.IDAntigravity:
		root, ext = filepath.Join(home, ".gemini", "antigravity-cli", "conversations"), ".pb"
	default:
		return fmt.Errorf("unknown agent %q — refusing to delete %s", c.Agent, c.Path)
	}
	clean := filepath.Clean(c.Path)
	// HasPrefix on the root + separator: prevents "/a/.claude/projects-evil"
	// from matching the "/a/.claude/projects" root.
	if !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return fmt.Errorf("refusing to delete %s — not under %s", clean, root)
	}
	if !strings.HasSuffix(clean, ext) {
		return fmt.Errorf("refusing to delete %s — not a %s transcript (%s)", clean, c.Agent, ext)
	}
	return nil
}

// Options modulates a List call. Zero value works (no limit, default
// home dir resolution).
type Options struct {
	// HomeDir overrides $HOME for transcript-directory resolution.
	// Empty falls back to os.UserHomeDir. Tests pass a tempdir.
	HomeDir string

	// Limit caps the number of conversations returned across all
	// agents AFTER sorting. 0 means no limit. Useful for the dashboard
	// "recent" panel; the full Conversations screen passes 0.
	Limit int

	// Since filters out conversations whose LastActivity is older than
	// `time.Now().Sub(Since)` durations. Zero means no filter.
	Since time.Duration

	// ExcludeHeadless drops conversations launched in headless / SDK
	// mode (anything where Conversation.IsHeadless reports true).
	// These accumulate fast when users wire Claude into automation —
	// shell scripts, watchers, the SDK — so the TUI and CLI hide
	// them by default to keep the conversations list usable as a
	// record of interactive work. Set true at the caller to apply.
	//
	// The package itself stays policy-neutral: zero value preserves
	// the existing "show everything" behavior so external callers
	// don't silently lose rows on upgrade. The TUI / CLI pass true
	// (overridable by the user) — see internal/config.ConversationsConfig
	// and the `--include-headless` flag on `ccmux list-conversations`.
	ExcludeHeadless bool
}

// All returns conversations from every supported agent, sorted by
// recency (most recent first). Per-agent walker errors are swallowed
// individually so a corrupted Claude transcript doesn't hide the
// user's Codex history. The returned error is non-nil only when ALL
// walkers failed — that's a sign of a deeper environment problem.
func All(opts Options) ([]Conversation, error) {
	if opts.HomeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home: %w", err)
		}
		opts.HomeDir = h
	}

	var all []Conversation
	var lastErr error
	walkerOK := 0
	for _, fn := range []func(string) ([]Conversation, error){
		ListClaude,
		ListCodex,
		ListAntigravity,
	} {
		got, err := fn(opts.HomeDir)
		if err != nil {
			lastErr = err
			continue
		}
		walkerOK++
		all = append(all, got...)
	}
	if walkerOK == 0 && lastErr != nil {
		return nil, fmt.Errorf("all walkers failed: %w", lastErr)
	}

	// Sort most-recent first.
	sort.Slice(all, func(i, j int) bool {
		return all[i].LastActivity.After(all[j].LastActivity)
	})

	if opts.Since > 0 {
		cutoff := time.Now().Add(-opts.Since)
		filtered := all[:0]
		for _, c := range all {
			if c.LastActivity.After(cutoff) {
				filtered = append(filtered, c)
			}
		}
		all = filtered
	}

	if opts.ExcludeHeadless {
		filtered := all[:0]
		for _, c := range all {
			if !c.IsHeadless() {
				filtered = append(filtered, c)
			}
		}
		all = filtered
	}

	if opts.Limit > 0 && len(all) > opts.Limit {
		all = all[:opts.Limit]
	}

	return all, nil
}

// ListClaude walks ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl. Each
// .jsonl file is one Claude conversation. Project label is derived
// from the encoded-cwd directory; preview is the first user prompt.
//
// Missing tree returns (nil, nil) — a fresh install where the user
// hasn't run Claude yet shouldn't surface as an error.
func ListClaude(home string) ([]Conversation, error) {
	root := filepath.Join(home, ".claude", "projects")
	dirs, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", root, err)
	}

	var out []Conversation
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		project := decodeClaudeProject(d.Name())
		projectDir := filepath.Join(root, d.Name())
		files, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(projectDir, f.Name())
			c := readClaudeTranscript(path, project)
			if c.ID == "" {
				continue
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// readClaudeTranscript scans one .jsonl transcript and returns the
// Conversation row for it. Returns a zero Conversation (empty ID) if
// the file can't be opened or doesn't yield a recognizable session
// ID — caller treats that as "skip this file."
func readClaudeTranscript(path, project string) Conversation {
	c := Conversation{
		Agent:   agent.IDClaude,
		Project: project,
		Path:    path,
	}
	// UUID from filename: <uuid>.jsonl
	base := filepath.Base(path)
	c.ID = strings.TrimSuffix(base, ".jsonl")

	f, err := os.Open(path)
	if err != nil {
		// Fall back to mtime when we can't read the file at all.
		if info, statErr := os.Stat(path); statErr == nil {
			c.LastActivity = info.ModTime()
		}
		return c
	}
	defer f.Close()

	// We need three things from the transcript: the real working
	// directory (from the cwd field on the first user event), the
	// first user prompt (for Preview), and the latest event
	// timestamp. Event timestamps take priority over mtime so a
	// stale transcript touched by a backup tool doesn't jump to the
	// top of the recent list.
	var latestEvent time.Time
	var cwdFromTranscript string
	sc := bufio.NewScanner(f)
	// Claude transcripts can have long lines (tool outputs etc.); bump
	// the buffer well past bufio's 64KB default.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev claudeEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type == "user" {
			// The cwd field on user events is the authoritative project
			// path. It's more reliable than the directory-name decode
			// (which uses '-' for both '/' and literal hyphens, making
			// the decode ambiguous for paths like ~/Projects/my-app).
			if ev.Cwd != "" && cwdFromTranscript == "" {
				cwdFromTranscript = ev.Cwd
			}
			if c.Preview == "" {
				c.Preview = truncatedPreview(ev.MessageContent())
			}
			// First user event with an entrypoint wins. Claude tags
			// every user event identically within a session, so we
			// don't need to scan past the first hit.
			if c.Entrypoint == "" && ev.Entrypoint != "" {
				c.Entrypoint = ev.Entrypoint
			}
		}
		if ts := ev.Timestamp(); !ts.IsZero() && ts.After(latestEvent) {
			latestEvent = ts
		}
	}
	// Override the decoded-path fallback with the authoritative cwd
	// from the transcript, if we found one.
	if cwdFromTranscript != "" {
		c.Project = cwdFromTranscript
	}

	if !latestEvent.IsZero() {
		c.LastActivity = latestEvent
	} else if info, err := os.Stat(path); err == nil {
		// No timestamped events — fall back to mtime so the row still
		// has a reasonable sort key.
		c.LastActivity = info.ModTime()
	}
	return c
}

// claudeEvent is a permissive view over Claude's transcript-line
// shape. We only care about `type`, the user message body, and a
// timestamp; everything else (tool calls, snapshots, etc.) goes
// through as-is and is ignored.
type claudeEvent struct {
	Type       string          `json:"type"`
	Message    json.RawMessage `json:"message"`
	Timestamp_ string          `json:"timestamp"` //nolint:revive
	// Cwd is present on user events and records the working directory
	// at the time of the prompt. More reliable than decoding the
	// project directory name (which is a lossy encoding of the path).
	Cwd string `json:"cwd"`
	// Entrypoint is Claude's launch-mode tag, present on user events.
	// "cli" for interactive sessions, "sdk-cli" for headless / SDK
	// runs (`claude -p`, the SDK, automation wrappers). Drives the
	// default "hide automation noise" filter — see Conversation.IsHeadless.
	Entrypoint string `json:"entrypoint"`
}

// MessageContent extracts the user-visible text from the embedded
// message field. Claude's message.content is either a string or an
// array of {type:"text", text:...} parts. Handle both shapes.
func (e claudeEvent) MessageContent() string {
	if len(e.Message) == 0 {
		return ""
	}
	var m struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(e.Message, &m); err != nil {
		return ""
	}
	// Try string form first.
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	// Otherwise array-of-parts form.
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				if b.Len() > 0 {
					b.WriteString(" ")
				}
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

// Timestamp returns the parsed event timestamp, or zero when missing
// or malformed.
func (e claudeEvent) Timestamp() time.Time {
	if e.Timestamp_ == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, e.Timestamp_)
	if err != nil {
		return time.Time{}
	}
	return t
}

// decodeClaudeProject turns Claude's encoded directory name back into
// the original cwd. Claude replaces '/' with '-' in the encoding, so
// "/Users/skz/Projects/ccmux" becomes "-Users-skz-Projects-ccmux".
// We can't perfectly recover the original (a literal '-' in a path
// segment would have been encoded to '-' too), but the result is good
// enough as a label.
func decodeClaudeProject(encoded string) string {
	// Heuristic reverse: a leading '-' means an absolute path.
	if strings.HasPrefix(encoded, "-") {
		return "/" + strings.ReplaceAll(encoded[1:], "-", "/")
	}
	return strings.ReplaceAll(encoded, "-", "/")
}

// ListCodex walks ~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-<ts>-<uuid>.jsonl.
// Filename pattern is documented in internal/codexusage; we just need
// the UUID + timestamp for the Conversation row, plus the first user
// message for Preview.
func ListCodex(home string) ([]Conversation, error) {
	root := filepath.Join(home, ".codex", "sessions")
	var out []Conversation
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return nil // tolerate per-file walk errors
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		c := readCodexTranscript(path)
		if c.ID != "" {
			out = append(out, c)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return out, nil
}

// readCodexTranscript: filename `rollout-<RFC3339-ish>-<uuid>.jsonl`.
// UUID is everything after the last dash before .jsonl. Timestamp is
// the segment between `rollout-` and the UUID, or mtime as fallback.
func readCodexTranscript(path string) Conversation {
	c := Conversation{
		Agent: agent.IDCodex,
		Path:  path,
	}
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	base = strings.TrimPrefix(base, "rollout-")
	// base now looks like: 2026-05-06T13-48-09-019dff0c-4b4d-7830-af27-408791f87129
	// The UUID is the last 5 dash-separated chunks (e.g. 019dff0c-4b4d-7830-af27-408791f87129).
	parts := strings.Split(base, "-")
	if len(parts) >= 5 {
		c.ID = strings.Join(parts[len(parts)-5:], "-")
	}
	if info, err := os.Stat(path); err == nil {
		c.LastActivity = info.ModTime()
	}

	// Read the file for first user prompt + project hint.
	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev codexEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		// session_meta is always the first event in a Codex rollout
		// and carries the launch-mode tag in payload.originator
		// ("codex_exec" for headless `codex exec`, "codex-tui" for an
		// interactive session). Capture once; subsequent events don't
		// re-state it.
		if ev.Type == "session_meta" && c.Entrypoint == "" && ev.Payload.Originator != "" {
			c.Entrypoint = ev.Payload.Originator
		}
		if ev.Cwd != "" && c.Project == "" {
			c.Project = ev.Cwd
		}
		if ev.Type == "user_message" && c.Preview == "" {
			c.Preview = truncatedPreview(ev.Text)
		}
		// Stop scanning once we have entrypoint, project, and preview.
		if c.Entrypoint != "" && c.Project != "" && c.Preview != "" {
			break
		}
	}
	return c
}

// codexEvent — permissive view over Codex's rollout-event shape.
// Only the fields we need for the conversation row.
type codexEvent struct {
	Type    string            `json:"type"`
	Text    string            `json:"text"`
	Cwd     string            `json:"cwd"`
	Payload codexEventPayload `json:"payload"`
}

// codexEventPayload — the nested payload Codex wraps every event in.
// session_meta's payload carries the originator + source fields that
// distinguish headless `codex exec` from interactive `codex` runs.
type codexEventPayload struct {
	// Originator is Codex's launch-mode tag on the session_meta event:
	// "codex_exec" for headless `codex exec`, "codex-tui" for the
	// interactive TUI. Mirrors Claude's `entrypoint` field; surfaces
	// on Conversation.Entrypoint and drives IsHeadless.
	Originator string `json:"originator"`
}

// ListAntigravity walks ~/.gemini/antigravity-cli/conversations/<uuid>.pb.
// We can't parse protobuf without a schema, but the filename is the
// UUID and the mtime is a useful "last activity" surrogate. Preview
// stays empty for these rows.
func ListAntigravity(home string) ([]Conversation, error) {
	root := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", root, err)
	}
	var out []Conversation
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pb") {
			continue
		}
		path := filepath.Join(root, e.Name())
		c := Conversation{
			Agent: agent.IDAntigravity,
			ID:    strings.TrimSuffix(e.Name(), ".pb"),
			Path:  path,
		}
		if info, err := e.Info(); err == nil {
			c.LastActivity = info.ModTime()
		}
		out = append(out, c)
	}
	return out, nil
}

// truncatedPreview turns an arbitrary prompt body into a single-line,
// length-capped preview suitable for a list row. Newlines collapse to
// spaces so a multi-line first prompt doesn't wreck row alignment.
func truncatedPreview(s string) string {
	s = strings.TrimSpace(s)
	// Collapse all runs of whitespace (incl. newlines) to a single
	// space — list rendering wants a one-liner.
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	out := b.String()
	// maxLen is in RUNES not bytes — slicing []byte at maxLen would
	// split a multi-byte char mid-sequence and produce garbage. Walk
	// the string rune-by-rune and stop at maxLen-1, then append the
	// ellipsis.
	const maxLen = 120
	runes := []rune(out)
	if len(runes) > maxLen {
		out = string(runes[:maxLen-1]) + "…"
	}
	return out
}
