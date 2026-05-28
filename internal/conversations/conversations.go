// Package conversations enumerates past agent conversations on disk —
// every Claude / Codex / Cursor / Antigravity session the user has had,
// regardless of whether ccmux launched it. It's the read side of the
// "resume an old conversation" flow: ccmux's TUI gets a unified
// timeline, ccmux's CLI exposes `ccmux list-conversations`, and the
// resume action launches the right agent with the right CLI flag.
//
// Each agent stores transcripts differently:
//
//	Claude:       ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl
//	              ~/.claude/projects/<encoded-cwd>/<uuid>/subagents/*.jsonl
//	Codex:        ~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-<ts>-<uuid>.jsonl
//	Cursor:       ~/.cursor/projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl
//	Antigravity:  ~/.gemini/antigravity-cli/conversations/<uuid>.pb
//	              ~/.gemini/tmp/<project-hash>/chats/session-*.json
//
// Claude, Codex, and Cursor use JSONL. Known fragments for the same logical
// conversation are merged in memory: Claude parent/subagent files by
// parent UUID, Codex rollouts by rollout UUID. We parse the first user
// event to extract a short preview. Antigravity .pb transcripts remain
// opaque, but Gemini-style JSON chat transcripts can provide preview
// and message-count data.
//
// Resume contracts (the argv each agent expects):
//
//	Claude:       claude --resume <uuid>
//	Codex:        codex resume <uuid>
//	Cursor:       cursor-agent --resume <uuid>
//	Antigravity:  agy --conversation <uuid>
//
// All four accept the conversation by ID, so a user picking a row in
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
	// Cursor's encoded project directory or transcript content; empty
	// when unrecoverable.
	Project string

	// LastActivity is the most recent timestamp ccmux could derive.
	// Sources, in priority: last event in the transcript > file mtime.
	// Drives the default sort order (most-recent first).
	LastActivity time.Time

	// Preview is the first ~100 chars of the conversation's first
	// user prompt. Empty when the transcript format is opaque or the
	// first prompt is unrecoverable. Trimmed and single-line for display.
	Preview string

	// Path is the primary absolute filesystem path to the transcript.
	// For merged conversations this prefers the resumable parent
	// transcript over nested fragments. Kept for callers that expect
	// one path per row.
	Path string

	// Paths contains every transcript fragment that contributes to
	// this logical conversation. Empty means Path is the only
	// transcript. The agents still own the files on disk; ccmux only
	// merges them at read time.
	Paths []string `json:",omitempty"`

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
	//     ""           — always. The known transcript formats do not
	//                    expose a launch-mode tag.
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
//   - Antigravity → never (known transcript formats carry no signal)
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

// Message is one turn read out of a conversation's transcript. The
// fields are the minimum the transcript preview modal needs to render
// a "recent thread" — role, the user-visible text body, and the event
// timestamp when the source format records one.
type Message struct {
	Role      string
	Content   string
	Timestamp time.Time
}

// RecentMessages returns up to `limit` of the most recent user /
// assistant turns from c's transcript fragments, in chronological order
// (oldest-first). Used by the Conversations screen's transcript
// preview modal to show a focused recap of the conversation.
//
// Per-agent behaviour:
//
//   - Claude  — parses JSONL, accepts events whose type is "user" or
//     "assistant" and whose embedded message.content carries text.
//   - Codex   — parses JSONL `response_item` events whose payload is a
//     `message` shape with role "user" or "assistant".
//   - Cursor  — parses JSONL transcript lines with role + message content.
//   - Antigravity — parses Gemini-style JSON chat files when present;
//     opaque protobuf transcripts return an empty slice and nil error.
//
// The function tolerates per-line parse errors (an unknown event shape
// is silently skipped) so a partial transcript still renders.
func RecentMessages(c Conversation, limit int) ([]Message, error) {
	if limit <= 0 {
		return nil, nil
	}
	paths := transcriptPaths(c)
	if len(paths) == 0 {
		return nil, nil
	}
	var all []Message
	switch c.Agent {
	case agent.IDClaude:
		for _, path := range paths {
			msgs, err := readClaudeMessages(path, limit)
			if err != nil {
				return nil, err
			}
			all = append(all, msgs...)
		}
	case agent.IDCodex:
		for _, path := range paths {
			msgs, err := readCodexMessages(path, limit)
			if err != nil {
				return nil, err
			}
			all = append(all, msgs...)
		}
	case agent.IDCursor:
		for _, path := range paths {
			msgs, err := readCursorMessages(path, limit)
			if err != nil {
				return nil, err
			}
			all = append(all, msgs...)
		}
	case agent.IDAntigravity:
		for _, path := range paths {
			msgs, err := readAntigravityMessages(path, limit)
			if err != nil {
				return nil, err
			}
			all = append(all, msgs...)
		}
	default:
		return nil, nil
	}
	return recentMessageTail(all, limit), nil
}

func recentMessageTail(all []Message, limit int) []Message {
	sort.SliceStable(all, func(i, j int) bool {
		left, right := all[i].Timestamp, all[j].Timestamp
		switch {
		case left.IsZero() && right.IsZero():
			return false
		case left.IsZero():
			return true
		case right.IsZero():
			return false
		default:
			return left.Before(right)
		}
	})
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all
}

func readClaudeMessages(path string, limit int) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var all []Message
	for sc.Scan() {
		var ev claudeEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Type != "user" && ev.Type != "assistant" {
			continue
		}
		body := strings.TrimSpace(ev.MessageContent())
		if ev.Type == "user" {
			body = cleanPromptText(body)
		}
		if body == "" {
			continue
		}
		all = append(all, Message{
			Role:      ev.Type,
			Content:   body,
			Timestamp: ev.Timestamp(),
		})
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

func readCodexMessages(path string, limit int) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var all []Message
	for sc.Scan() {
		var ev codexEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		role, body := ev.MessageContent()
		if role != "user" && role != "assistant" {
			continue
		}
		if role == "user" {
			body = cleanPromptText(body)
		}
		if body == "" {
			continue
		}
		all = append(all, Message{
			Role:      role,
			Content:   body,
			Timestamp: ev.Timestamp(),
		})
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

func readCursorMessages(path string, limit int) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var all []Message
	for sc.Scan() {
		var ev cursorEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Role != "user" && ev.Role != "assistant" {
			continue
		}
		body := strings.TrimSpace(ev.MessageContent())
		if ev.Role == "user" {
			body = cleanPromptText(body)
		}
		if body == "" {
			continue
		}
		all = append(all, Message{
			Role:      ev.Role,
			Content:   body,
			Timestamp: ev.Timestamp(),
		})
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

func readAntigravityMessages(path string, limit int) ([]Message, error) {
	if !strings.HasSuffix(path, ".json") {
		return nil, nil
	}
	doc, err := readGeminiChat(path)
	if err != nil {
		return nil, err
	}
	all := make([]Message, 0, len(doc.Messages))
	for _, msg := range doc.Messages {
		role := geminiMessageRole(msg.Type)
		if role == "" {
			continue
		}
		body := strings.TrimSpace(msg.Content)
		if role == "user" {
			body = cleanPromptText(body)
		}
		if body == "" {
			continue
		}
		all = append(all, Message{
			Role:      role,
			Content:   body,
			Timestamp: parseRFC3339(msg.Timestamp),
		})
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

// CountMessages returns the number of user + assistant turns in c's
// transcript fragments. Used by the detail pane to surface a "thread length"
// signal without loading the full message bodies. Same per-agent
// handling as RecentMessages: Claude / Codex / Cursor parse JSONL,
// Antigravity parses JSON chats and returns 0 for opaque protobuf.
func CountMessages(c Conversation) (int, error) {
	paths := transcriptPaths(c)
	if len(paths) == 0 {
		return 0, nil
	}
	total := 0
	switch c.Agent {
	case agent.IDClaude:
		for _, path := range paths {
			n, err := countClaudeMessages(path)
			if err != nil {
				return 0, err
			}
			total += n
		}
	case agent.IDCodex:
		for _, path := range paths {
			n, err := countCodexMessages(path)
			if err != nil {
				return 0, err
			}
			total += n
		}
	case agent.IDCursor:
		for _, path := range paths {
			n, err := countCursorMessages(path)
			if err != nil {
				return 0, err
			}
			total += n
		}
	case agent.IDAntigravity:
		for _, path := range paths {
			n, err := countAntigravityMessages(path)
			if err != nil {
				return 0, err
			}
			total += n
		}
	default:
		return 0, nil
	}
	return total, nil
}

func countClaudeMessages(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(sc.Bytes(), &head); err != nil {
			continue
		}
		if head.Type == "user" || head.Type == "assistant" {
			n++
		}
	}
	return n, nil
}

func countCodexMessages(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		var ev codexEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		role, body := ev.MessageContent()
		if (role == "user" || role == "assistant") && strings.TrimSpace(body) != "" {
			n++
		}
	}
	return n, nil
}

func countCursorMessages(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		var ev cursorEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if (ev.Role == "user" || ev.Role == "assistant") && strings.TrimSpace(ev.MessageContent()) != "" {
			n++
		}
	}
	return n, nil
}

func countAntigravityMessages(path string) (int, error) {
	if !strings.HasSuffix(path, ".json") {
		return 0, nil
	}
	doc, err := readGeminiChat(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, msg := range doc.Messages {
		if geminiMessageRole(msg.Type) != "" && strings.TrimSpace(msg.Content) != "" {
			n++
		}
	}
	return n, nil
}

// Delete removes a conversation's transcript files from disk. This is
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
	paths := transcriptPaths(c)
	if len(paths) == 0 {
		return fmt.Errorf("conversation %q has no transcript path", c.ID)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	for _, path := range paths {
		if err := guardTranscriptPath(home, c.Agent, path); err != nil {
			return err
		}
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("delete transcript: %w", err)
		}
	}
	return nil
}

// guardTranscriptPath returns nil only when path is a plausible
// transcript file for agentID: under the agent's root directory and
// carrying the agent's file extension. Returns a descriptive error
// otherwise. filepath.Clean resolves any `..` so a path can't escape
// the root via traversal.
func guardTranscriptPath(home string, agentID agent.ID, path string) error {
	type transcriptRoot struct {
		root string
		ext  string
	}
	var allowed []transcriptRoot
	switch agentID {
	case agent.IDClaude:
		allowed = []transcriptRoot{{root: filepath.Join(home, ".claude", "projects"), ext: ".jsonl"}}
	case agent.IDCodex:
		allowed = []transcriptRoot{{root: filepath.Join(home, ".codex", "sessions"), ext: ".jsonl"}}
	case agent.IDCursor:
		allowed = []transcriptRoot{{root: filepath.Join(home, ".cursor", "projects"), ext: ".jsonl"}}
	case agent.IDAntigravity:
		allowed = []transcriptRoot{
			{root: filepath.Join(home, ".gemini", "antigravity-cli", "conversations"), ext: ".pb"},
			{root: filepath.Join(home, ".gemini", "tmp"), ext: ".json"},
		}
	default:
		return fmt.Errorf("unknown agent %q — refusing to delete %s", agentID, path)
	}
	clean := filepath.Clean(path)
	// HasPrefix on the root + separator: prevents "/a/.claude/projects-evil"
	// from matching the "/a/.claude/projects" root.
	for _, candidate := range allowed {
		if strings.HasPrefix(clean, candidate.root+string(filepath.Separator)) &&
			strings.HasSuffix(clean, candidate.ext) {
			return nil
		}
	}
	return fmt.Errorf("refusing to delete %s — not under a known %s transcript root", clean, agentID)
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
		ListCursor,
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

// ListClaude walks ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl and
// nested ~/.claude/projects/<encoded-cwd>/<uuid>/subagents/*.jsonl.
// The returned row is the logical parent conversation: scattered
// fragments with the same parent UUID are merged in memory. Project
// label is derived from the encoded-cwd directory; preview is the
// first user prompt.
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
		err := filepath.WalkDir(projectDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				if path == projectDir {
					return nil
				}
				if shouldWalkClaudeDir(projectDir, path) {
					return nil
				}
				return filepath.SkipDir
			}
			if !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			id, ok := claudeLogicalID(projectDir, path)
			if !ok {
				return nil
			}
			c := readClaudeTranscript(path, project)
			c.ID = id
			if c.ID == "" {
				return nil
			}
			out = append(out, c)
			return nil
		})
		if err != nil {
			continue
		}
	}
	return mergeConversations(out), nil
}

func shouldWalkClaudeDir(projectDir, path string) bool {
	rel, err := filepath.Rel(projectDir, path)
	if err != nil || rel == "." {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return len(parts) == 1 || (len(parts) == 2 && parts[1] == "subagents")
}

func claudeLogicalID(projectDir, path string) (string, bool) {
	rel, err := filepath.Rel(projectDir, path)
	if err != nil || rel == "." {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	switch {
	case len(parts) == 1:
		return strings.TrimSuffix(parts[0], ".jsonl"), true
	case len(parts) == 3 && parts[1] == "subagents":
		return parts[0], true
	default:
		return "", false
	}
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
	return parseRFC3339(e.Timestamp_)
}

func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func messagePartsContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type != "text" && p.Type != "input_text" && p.Type != "output_text" {
				continue
			}
			t := strings.TrimSpace(p.Text)
			if t == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(t)
		}
		return b.String()
	}
	return ""
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
	return mergeConversations(out), nil
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
	var latestEvent time.Time
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
		meta := ev.Metadata()
		if ev.Type == "session_meta" && c.Entrypoint == "" && meta.Originator != "" {
			c.Entrypoint = meta.Originator
		}
		if ev.Cwd != "" && c.Project == "" {
			c.Project = ev.Cwd
		}
		if meta.Cwd != "" && c.Project == "" {
			c.Project = meta.Cwd
		}
		if role, body := ev.MessageContent(); role == "user" && c.Preview == "" {
			c.Preview = truncatedPreview(cleanPromptText(body))
		}
		if ts := ev.Timestamp(); !ts.IsZero() && ts.After(latestEvent) {
			latestEvent = ts
		}
	}
	if !latestEvent.IsZero() {
		c.LastActivity = latestEvent
	}
	return c
}

// codexEvent — permissive view over Codex's rollout-event shape.
// Only the fields we need for the conversation row.
type codexEvent struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	Cwd        string          `json:"cwd"`
	Timestamp_ string          `json:"timestamp"` //nolint:revive
	Payload    json.RawMessage `json:"payload"`
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
	Cwd        string `json:"cwd"`
}

func (e codexEvent) Metadata() codexEventPayload {
	var p codexEventPayload
	_ = json.Unmarshal(e.Payload, &p)
	return p
}

func (e codexEvent) Timestamp() time.Time {
	return parseRFC3339(e.Timestamp_)
}

func (e codexEvent) MessageContent() (string, string) {
	switch e.Type {
	case "user_message":
		return "user", strings.TrimSpace(e.Text)
	case "response_item":
		var p struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return "", ""
		}
		if p.Type != "message" || (p.Role != "user" && p.Role != "assistant") {
			return "", ""
		}
		var b strings.Builder
		for _, part := range p.Content {
			if part.Type != "input_text" && part.Type != "text" && part.Type != "output_text" {
				continue
			}
			t := strings.TrimSpace(part.Text)
			if t == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(t)
		}
		return p.Role, strings.TrimSpace(b.String())
	default:
		return "", ""
	}
}

// ListCursor walks ~/.cursor/projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl.
// Cursor's JSONL carries role + message content. The project label is
// derived from Cursor's encoded project directory.
func ListCursor(home string) ([]Conversation, error) {
	root := filepath.Join(home, ".cursor", "projects")
	var out []Conversation
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		project, id, ok := cursorConversationPath(root, path)
		if !ok {
			return nil
		}
		c := readCursorTranscript(path, project)
		c.ID = id
		if c.ID != "" {
			out = append(out, c)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return mergeConversations(out), nil
}

func cursorConversationPath(root, path string) (project, id string, ok bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return "", "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 4 || parts[1] != "agent-transcripts" {
		return "", "", false
	}
	id = strings.TrimSuffix(parts[3], ".jsonl")
	if id == "" || parts[2] != id {
		return "", "", false
	}
	return decodeCursorProject(parts[0]), id, true
}

func decodeCursorProject(encoded string) string {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return ""
	}
	if strings.HasPrefix(encoded, "-") {
		return "/" + strings.ReplaceAll(encoded[1:], "-", "/")
	}
	return "/" + strings.ReplaceAll(encoded, "-", "/")
}

func readCursorTranscript(path, project string) Conversation {
	c := Conversation{
		Agent:   agent.IDCursor,
		Project: project,
		Path:    path,
	}
	if info, err := os.Stat(path); err == nil {
		c.LastActivity = info.ModTime()
	}
	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()
	var latestEvent time.Time
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev cursorEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Role == "user" && c.Preview == "" {
			c.Preview = truncatedPreview(cleanPromptText(ev.MessageContent()))
		}
		if ts := ev.Timestamp(); !ts.IsZero() && ts.After(latestEvent) {
			latestEvent = ts
		}
	}
	if !latestEvent.IsZero() {
		c.LastActivity = latestEvent
	}
	return c
}

type cursorEvent struct {
	Role       string          `json:"role"`
	Message    json.RawMessage `json:"message"`
	Timestamp_ string          `json:"timestamp"` //nolint:revive
}

func (e cursorEvent) Timestamp() time.Time {
	return parseRFC3339(e.Timestamp_)
}

func (e cursorEvent) MessageContent() string {
	if len(e.Message) == 0 {
		return ""
	}
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(e.Message, &m); err != nil {
		return ""
	}
	return messagePartsContent(m.Content)
}

// ListAntigravity walks ~/.gemini/antigravity-cli/conversations/<uuid>.pb.
// We can't parse protobuf without a schema, but the filename is the
// UUID and the mtime is a useful "last activity" surrogate. Preview
// stays empty for these rows. Gemini-style JSON chat files under
// ~/.gemini/tmp are also listed when present, with full preview and
// message-count support.
func ListAntigravity(home string) ([]Conversation, error) {
	var out []Conversation
	pb, err := listAntigravityPB(home)
	if err != nil {
		return nil, err
	}
	out = append(out, pb...)
	jsonChats, err := listGeminiJSONChats(home)
	if err != nil {
		return nil, err
	}
	out = append(out, jsonChats...)
	return mergeConversations(out), nil
}

func listAntigravityPB(home string) ([]Conversation, error) {
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

func listGeminiJSONChats(home string) ([]Conversation, error) {
	root := filepath.Join(home, ".gemini", "tmp")
	var out []Conversation
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasPrefix(d.Name(), "session-") || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		if !isGeminiChatPath(root, path) {
			return nil
		}
		c := readGeminiChatConversation(path)
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

func isGeminiChatPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return len(parts) == 3 && parts[1] == "chats"
}

type geminiChat struct {
	SessionID   string          `json:"sessionId"`
	ProjectHash string          `json:"projectHash"`
	LastUpdated string          `json:"lastUpdated"`
	StartTime   string          `json:"startTime"`
	Messages    []geminiMessage `json:"messages"`
}

type geminiMessage struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

func readGeminiChat(path string) (geminiChat, error) {
	var doc geminiChat
	b, err := os.ReadFile(path)
	if err != nil {
		return doc, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return doc, fmt.Errorf("parse %s: %w", path, err)
	}
	return doc, nil
}

func readGeminiChatConversation(path string) Conversation {
	c := Conversation{
		Agent: agent.IDAntigravity,
		Path:  path,
	}
	if info, err := os.Stat(path); err == nil {
		c.LastActivity = info.ModTime()
	}
	doc, err := readGeminiChat(path)
	if err != nil {
		c.ID = strings.TrimSuffix(filepath.Base(path), ".json")
		return c
	}
	c.ID = doc.SessionID
	if c.ID == "" {
		c.ID = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	if doc.ProjectHash != "" {
		c.Project = "project " + shortHash(doc.ProjectHash)
	}
	if ts := parseRFC3339(doc.LastUpdated); !ts.IsZero() {
		c.LastActivity = ts
	} else if ts := parseRFC3339(doc.StartTime); !ts.IsZero() {
		c.LastActivity = ts
	}
	for _, msg := range doc.Messages {
		if msg.Type != "user" {
			continue
		}
		c.Preview = truncatedPreview(cleanPromptText(msg.Content))
		if c.Preview != "" {
			break
		}
	}
	return c
}

func geminiMessageRole(t string) string {
	switch t {
	case "user":
		return "user"
	case "gemini", "assistant", "model":
		return "assistant"
	default:
		return ""
	}
}

func shortHash(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

func mergeConversations(list []Conversation) []Conversation {
	if len(list) < 2 {
		return list
	}
	merged := make([]Conversation, 0, len(list))
	byKey := map[string]int{}
	for _, c := range list {
		key := string(c.Agent) + "\x00" + c.ID
		if idx, ok := byKey[key]; ok {
			mergeConversation(&merged[idx], c)
			continue
		}
		c.Paths = nil
		merged = append(merged, c)
		byKey[key] = len(merged) - 1
	}
	for i := range merged {
		merged[i].Paths = compactTranscriptPaths(merged[i].Path, merged[i].Paths)
		if len(merged[i].Paths) < 2 {
			merged[i].Paths = nil
		}
	}
	return merged
}

func mergeConversation(dst *Conversation, src Conversation) {
	dst.Paths = compactTranscriptPaths(dst.Path, append(dst.Paths, transcriptPaths(src)...))
	srcPrimary := preferPrimaryPath(src.Agent, dst.Path, src.Path)
	if dst.Path == "" || srcPrimary {
		dst.Path = src.Path
	}
	if dst.Project == "" || (srcPrimary && src.Project != "") {
		dst.Project = src.Project
	}
	if dst.Preview == "" || (srcPrimary && src.Preview != "") {
		dst.Preview = src.Preview
	}
	if src.Entrypoint != "" {
		if shouldReplaceEntrypoint(dst.Agent, dst.Entrypoint, src.Entrypoint) {
			dst.Entrypoint = src.Entrypoint
		}
	}
	if src.LastActivity.After(dst.LastActivity) {
		dst.LastActivity = src.LastActivity
	}
	dst.Paths = compactTranscriptPaths(dst.Path, dst.Paths)
}

func shouldReplaceEntrypoint(agentID agent.ID, current, candidate string) bool {
	if candidate == "" {
		return false
	}
	if current == "" {
		return true
	}
	currentHeadless := Conversation{Agent: agentID, Entrypoint: current}.IsHeadless()
	candidateHeadless := Conversation{Agent: agentID, Entrypoint: candidate}.IsHeadless()
	return currentHeadless && !candidateHeadless
}

func preferPrimaryPath(agentID agent.ID, current, candidate string) bool {
	if candidate == "" || current == "" {
		return candidate != ""
	}
	if agentID != agent.IDClaude {
		return false
	}
	return isClaudeSubagentPath(current) && !isClaudeSubagentPath(candidate)
}

func isClaudeSubagentPath(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/subagents/")
}

func transcriptPaths(c Conversation) []string {
	return compactTranscriptPaths(c.Path, c.Paths)
}

func compactTranscriptPaths(primary string, paths []string) []string {
	out := make([]string, 0, len(paths)+1)
	seen := map[string]struct{}{}
	if primary != "" {
		out = append(out, primary)
		seen[primary] = struct{}{}
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		out = append(out, path)
		seen[path] = struct{}{}
	}
	return out
}

// cleanPromptText strips synthetic CLI noise from a raw user-message
// body so the conversation preview reflects what the user actually
// typed, not the wrappers each agent injects around it. Two passes:
//
//   - Drop "pure-noise" blocks entirely (open tag + content + close).
//     environment_context / user_instructions are state dumps the CLI
//     prepends to every session; surfacing them as a "first prompt"
//     would just show cwd / shell info.
//   - Drop leading Codex AGENTS.md instruction bundles that are
//     persisted as user input before the real prompt.
//   - For everything else, remove XML-style tag delimiters but keep
//     the inner content. system-reminder, command-message, etc. carry
//     user-meaningful text once the angle brackets are gone.
//   - Drop leading skill-invocation wrappers such as
//     "worktree-openspec-workflow\n/worktree-openspec-workflow …" and
//     keep only the prompt text after the command token.
//
// Returns "" when the input is empty after cleaning — callers should
// treat that as "skip this message" and continue scanning the
// transcript for the next eligible user turn.
func cleanPromptText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = removeLeadingAgentsInstructions(s)
	if s == "" {
		return ""
	}
	for _, tag := range []string{"environment_context", "user_instructions"} {
		s = removeXMLBlock(s, tag)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.TrimSpace(stripXMLTags(s))
	return stripLeadingSkillInvocation(s)
}

func removeLeadingAgentsInstructions(s string) string {
	s = strings.TrimSpace(s)
	const header = "# AGENTS.md instructions"
	if !strings.HasPrefix(s, header) {
		return s
	}
	const closeTag = "</INSTRUCTIONS>"
	end := strings.Index(s, closeTag)
	if end < 0 {
		return s
	}
	return strings.TrimSpace(s[end+len(closeTag):])
}

// removeXMLBlock removes every <tag>…</tag> occurrence (including the
// body) from s. Used to drop pure-noise CLI wrappers like
// <environment_context>cwd=…</environment_context>.
func removeXMLBlock(s, tag string) string {
	open := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], closeTag)
		if j < 0 {
			return s
		}
		s = s[:i] + s[i+j+len(closeTag):]
	}
}

// stripXMLTags removes every <...> tag delimiter from s while keeping
// the inner text. Naive scan — not an XML parser. Good enough for the
// agent-injected wrappers we care about (system-reminder,
// command-message, command-name, …).
func stripXMLTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func stripLeadingSkillInvocation(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if stripped, ok := stripLeadingSkillHeaderLine(s); ok {
		return stripped
	}
	fields := strings.Fields(s)
	if len(fields) >= 2 && isSkillInvocationName(fields[0]) {
		if commandName, ok := slashCommandName(fields[1]); ok && commandName == fields[0] {
			rest := strings.TrimSpace(strings.TrimPrefix(s, fields[0]))
			if stripped, ok := stripLeadingSlashCommand(rest, commandName); ok {
				return stripped
			}
		}
	}
	if stripped, ok := stripLeadingSlashCommand(s, ""); ok {
		return stripped
	}
	return s
}

func stripLeadingSkillHeaderLine(s string) (string, bool) {
	lines := strings.Split(s, "\n")
	firstIdx := firstNonEmptyLine(lines, 0)
	if firstIdx < 0 {
		return "", true
	}
	name := strings.TrimSpace(lines[firstIdx])
	if !isSkillInvocationName(name) {
		return "", false
	}
	nextIdx := firstNonEmptyLine(lines, firstIdx+1)
	if nextIdx < 0 {
		return "", false
	}
	next := strings.TrimSpace(lines[nextIdx])
	strippedNext, ok := stripLeadingSlashCommand(next, name)
	if !ok {
		return "", false
	}
	out := append([]string{strippedNext}, lines[nextIdx+1:]...)
	return strings.TrimSpace(strings.Join(out, "\n")), true
}

func firstNonEmptyLine(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
	}
	return -1
}

func stripLeadingSlashCommand(s, expectedName string) (string, bool) {
	s = strings.TrimSpace(s)
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", true
	}
	name, ok := slashCommandName(fields[0])
	if !ok {
		return "", false
	}
	if expectedName != "" && name != expectedName {
		return "", false
	}
	if expectedName == "" && !isSkillInvocationName(name) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(s, fields[0])), true
}

func slashCommandName(token string) (string, bool) {
	if !strings.HasPrefix(token, "/") {
		return "", false
	}
	name := strings.TrimPrefix(token, "/")
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	if !isCommandName(name) {
		return "", false
	}
	return name, true
}

func isSkillInvocationName(s string) bool {
	return strings.Contains(s, "-") && isCommandName(s)
}

func isCommandName(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '-' || r == '_' {
			continue
		}
		return false
	}
	return s != ""
}

// truncatedPreview turns an arbitrary prompt body into a single-line,
// length-capped preview suitable for a list row. Newlines collapse to
// spaces so a multi-line first prompt doesn't wreck row alignment.
// Strips agent-injected XML wrappers via cleanPromptText so the
// preview reflects what the user actually typed, not the synthetic
// context the CLI wraps around it.
func truncatedPreview(s string) string {
	s = cleanPromptText(s)
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
	//
	// 400 chars is enough for the detail pane to wrap a couple of
	// paragraphs while keeping the in-memory cost trivial; list rows
	// do their own width-truncation downstream of this cap.
	const maxLen = 400
	runes := []rune(out)
	if len(runes) > maxLen {
		out = string(runes[:maxLen-1]) + "…"
	}
	return out
}
