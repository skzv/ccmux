## ADDED Requirements

### Requirement: Per-agent palette as single source of truth

The TUI SHALL expose a single, theme-agnostic helper that maps an `agent.ID` to a `lipgloss.Style` representing that agent's accent colour. Every surface that surfaces multiple agents (the Dashboard's Usage panel, the Conversations section nav, the Conversations row's agent label column, the Agents sub-tab row) SHALL consume the colour from this helper. Inline per-screen colour selection for agents SHALL NOT be reintroduced.

The mapping SHALL be: Claude=`Palette.Mauve`, Codex=`Palette.Sky`, Antigravity=`Palette.Peach`, Cursor=`Palette.Teal`.

#### Scenario: Agent accent helper exists

- **WHEN** a screen file imports `internal/tui/styles`
- **THEN** a `styles.AgentAccent(id agent.ID) lipgloss.Style` (or equivalent) is callable and returns the agent's accent style

#### Scenario: Conversations section nav uses the helper

- **WHEN** the Conversations screen renders `renderAgentNav`
- **THEN** the active section's label colour is produced by the shared helper for the matching agent ID, not by an inline `lipgloss.NewStyle().Foreground(…)` literal

### Requirement: Conversation row agent labels carry the agent accent

Each conversation row's agent label column SHALL render in the agent's accent colour (via the shared helper). Agent labels SHALL use bare lowercase names (`claude`, `codex`, `cursor`, `agy`) without bracket adornment. The row SHALL keep the agent label, compact timestamp, and prompt preview separated by compact fixed gaps, avoiding wide padded columns. The rest of the row (timestamp, preview) SHALL stay in the default / muted foreground.

#### Scenario: Codex row's agent label renders in sky

- **WHEN** the Conversations list renders a row whose `Conversation.Agent` is `agent.IDCodex`
- **THEN** the agent label (`codex`) renders in the colour returned by the agent-accent helper for Codex
- **AND** the label is not bracketed as `[codex]`

#### Scenario: Row columns are compact

- **GIVEN** a Codex conversation whose compact timestamp is `now`
- **WHEN** the Conversations list renders the row
- **THEN** the visible row text separates `codex`, `now`, and the prompt preview with compact gaps instead of wide padded columns

### Requirement: Armed-for-delete chip

When a row's `c.ID == m.pendingDelete`, the row SHALL render an `[delete? x to confirm · esc]` chip at the row's trailing edge in the `Semantic.Danger` colour. The agent label, timestamp, and a truncated preview SHALL remain visible on the same row.

#### Scenario: Armed row keeps identity visible

- **WHEN** the user presses `x` to arm delete on a conversation
- **THEN** the row displays the agent label, timestamp, a truncated preview, and the `[delete? x to confirm · esc]` chip — all on one row

### Requirement: Conversations columns do not bleed

In wide mode, the Conversations list column and detail column SHALL render as separate framed panes, matching the Projects and Notes two-column treatment. The columns SHALL reserve a visible gutter between panes, allocate roughly half the terminal width to the detail pane, and remain bounded to their allocated widths. Long project paths, skill paths, URLs, and first-prompt text in the detail pane SHALL wrap or hard-wrap within the detail column instead of relying on terminal soft wrapping.

#### Scenario: Long detail text stays inside the right column

- **GIVEN** a selected conversation has a long project path or first prompt containing path-like unbroken tokens
- **WHEN** the Conversations screen renders in wide mode
- **THEN** no rendered line exceeds the terminal width
- **AND** wrapped right-column text does not reflow beneath the left list

#### Scenario: Wide columns are framed with a gutter

- **WHEN** the Conversations screen renders in wide mode
- **THEN** the list and detail columns each render with pane borders
- **AND** a visible gutter separates the two bordered panes

### Requirement: Transcript preview modal

The Conversations screen SHALL bind the `p` key to open a focused overlay rendering the selected conversation's recent message thread (Glamour-rendered, last ~30 messages). The overlay SHALL close on `p` or `esc`. The overlay SHALL consume the design-system tokens for every rendered element.

#### Scenario: `p` opens the preview modal

- **WHEN** the user is on the Conversations screen with a conversation selected and presses `p`
- **THEN** the transcript-preview overlay opens and renders the recent messages

#### Scenario: Modal Glamour theme matches the design system

- **WHEN** the transcript-preview overlay renders markdown
- **THEN** code blocks, headings, links, and blockquotes use the same Glamour theme configuration the Notes preview pane uses (per the parallel `redesign-tui-notes` change)

### Requirement: Conversation previews show prompt text only

Conversation row previews, the detail pane's first-prompt block, and transcript-preview user turns SHALL strip agent-injected wrapper text before display. The data layer SHALL drop pure synthetic XML blocks such as `environment_context` and `user_instructions`, drop leading Codex `# AGENTS.md instructions ... <INSTRUCTIONS>...</INSTRUCTIONS>` bundles, strip XML tag delimiters from wrapper tags that contain useful body text, and remove leading skill-invocation prefixes such as `worktree-openspec-workflow /worktree-openspec-workflow` while preserving the prompt text after the command token.

#### Scenario: Skill invocation prefix is hidden

- **GIVEN** a Claude user transcript message begins with `worktree-openspec-workflow` followed by `/worktree-openspec-workflow help me create a spec`
- **WHEN** Conversations renders the row preview or first-prompt detail
- **THEN** the visible text starts with `help me create a spec`
- **AND** the skill name and slash-command token are not shown

#### Scenario: Codex AGENTS instructions are hidden

- **GIVEN** a Codex user transcript message begins with `# AGENTS.md instructions for /repo` and an `<INSTRUCTIONS>...</INSTRUCTIONS>` block before the real prompt
- **WHEN** Conversations renders the row preview or first-prompt detail
- **THEN** the visible text starts with the real prompt after the instructions bundle
- **AND** the AGENTS.md instructions are not shown

### Requirement: Scattered transcript fragments merge into one logical conversation

The Conversations data layer SHALL merge known transcript fragments in memory before the TUI, CLI list, project picker, preview modal, message count, recency sort, and delete flow consume them. It SHALL NOT rewrite, concatenate, move, or otherwise physically merge agent-owned transcript files on disk.

Claude fragments SHALL merge by parent session ID: `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl` and nested `~/.claude/projects/<encoded-cwd>/<uuid>/subagents/*.jsonl` SHALL produce one `Conversation` whose `ID` is `<uuid>`. Codex rollout fragments SHALL merge by rollout UUID when multiple `~/.codex/sessions/**/rollout-<timestamp>-<uuid>.jsonl` files share the same `<uuid>`.

Merged conversations SHALL keep `Conversation.Path` as the primary/resumable transcript path and expose every contributing fragment through `Conversation.Paths`. Recent-message preview and message counts SHALL read all fragments, sort timestamped messages chronologically, and return the requested recent tail. Delete SHALL validate every fragment path before removing any fragment, then remove all validated fragments for the selected logical conversation.

#### Scenario: Claude parent and subagent fragments render as one row

- **GIVEN** a Claude parent transcript `<uuid>.jsonl`
- **AND** one or more nested `<uuid>/subagents/*.jsonl` fragments
- **WHEN** Conversations loads the Claude transcript tree
- **THEN** the list contains one row for `<uuid>`
- **AND** the row's last activity, message count, preview modal, and delete action account for every fragment

#### Scenario: Duplicate Codex rollout UUIDs render as one row

- **GIVEN** two Codex rollout JSONL files with the same rollout UUID
- **WHEN** Conversations loads the Codex session tree
- **THEN** the list contains one row for that UUID
- **AND** the row remains resumable with `codex resume <uuid>`

### Requirement: Non-Claude transcript rows expose common conversation metadata

The Conversations data layer SHALL populate the same user-facing row/detail metadata for supported non-Claude JSON transcript formats that it populates for Claude: agent ID, resumable conversation ID, project label when recoverable, last activity, first-prompt preview, recent messages, and message count.

Codex SHALL read project cwd from either top-level `cwd` or `session_meta.payload.cwd`, last activity from timestamped transcript events when present, and first-prompt preview from either `user_message` events or user `response_item` messages. Cursor SHALL read JSONL transcripts from `~/.cursor/projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl`. Antigravity SHALL continue to list opaque `.pb` transcripts by ID/mtime and SHALL additionally read Gemini-style JSON chat transcripts from `~/.gemini/tmp/<project-hash>/chats/session-*.json` when present.

#### Scenario: Codex payload metadata renders like Claude metadata

- **GIVEN** a Codex rollout whose `session_meta.payload.cwd` is set and whose first user text is stored as a `response_item`
- **WHEN** Conversations loads Codex transcripts
- **THEN** the Codex row has a project label, first-prompt preview, last activity, recent messages, and message count

#### Scenario: Cursor JSONL transcripts render in the Cursor section

- **GIVEN** a Cursor Agent JSONL transcript under `~/.cursor/projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl`
- **WHEN** Conversations loads all agents
- **THEN** the Cursor section contains a resumable row with first-prompt preview, recent messages, and message count

#### Scenario: Antigravity JSON chats render with previews

- **GIVEN** a Gemini-style Antigravity JSON chat under `~/.gemini/tmp/<project-hash>/chats/session-*.json`
- **WHEN** Conversations loads Antigravity transcripts
- **THEN** the Agy section contains a row with first-prompt preview, recent messages, and message count
