## Context

Notes is the per-project markdown browser — a left column listing every `.md` file under the project (grouped by folder), a right column with the Glamour-rendered preview, and a search mode (`/`) plus a project picker (`p`).

PR #114 migrated all three selection sites (note rows, search hits, project picker rows) to `components.RenderListRow` and dropped the inline `p: switch project · / : search · …` hint row from `renderList`. Two pre-existing gaps surfaced during the cleanup audit:

- `n` (new note) is advertised in `helpForScreen[ScreenNotes]` and in the `(empty — press n to create a note)` placeholder. The handler has never existed. PR #114 dropped `n` from the HelpBar to stop falsely advertising a broken key. The Feature Catalog flagged the feature as P2.
- The Glamour preview pane uses default Glamour styles. The default greys, accent, and code-block treatment clash with the design-system palette — the pane reads as a third-party widget.

This change wires `n` (with the simpler one-flow version: name → write → `$EDITOR`) and themes Glamour to consume design-system tokens.

## Goals / Non-Goals

**Goals:**

- Make `n` work. Users have wanted this since the feature was first advertised.
- Make the Glamour preview pane visually part of ccmux (not GitHub-on-terminal).
- Apply the sub-section indent and chip vocabulary the rest of the TUI uses.
- Surface a note's H1 as the row label when available, so the list reads as a tree of titles rather than a tree of filenames.

**Non-Goals:**

- A multi-kind note picker (Agent Log / Spec / ADR). The original `helpForScreen` text described that; this change ships the simpler single-flow form.
- Ripgrep-search rewrite or search-result ranking changes.
- Obsidian deep linking changes.
- Frontmatter editing UI.

## Decisions

### New-note flow shape

**Decision:** `n` opens a Huh-based modal asking for filename (suggested default: `notes/note-YYYY-MM-DD-HHMM.md`) and an optional one-line title. On submit, write a minimal `# {title}\n\n` body to disk and open in `$EDITOR`.

**Rationale:** Smallest useful version. Filename collision is handled by the Huh validator. The user can type anything they want as filename; if they leave the default, the dated name avoids overwrites.

**Alternatives:**

- _Open `$EDITOR` directly on a generated filename._ No filename input UX; user has to rename later if they want a meaningful name.
- _Multi-kind picker._ Bigger feature; can be a follow-up.

### Glamour theme tune

**Decision:** Build a `glamour.Style` value from the design-system palette and pass it via `glamour.WithStyles(...)`. Mapping:

- Heading colours: `s.Type.Title` (mauve, bold) for H1; `s.Type.Subtitle` for H2/H3; muted for H4+.
- Code block bg: `s.P.BGAlt`. Code text: `s.P.Lavender`.
- Link: `s.Semantic.Accent`.
- Blockquote: `s.Muted` + leading bar in `s.Semantic.Accent`.

**Rationale:** Glamour exposes a style configuration that maps cleanly to our token set. The pane stops looking like GitHub markdown.

### H1 fallback for row labels

**Decision:** Extend `notes.Vault.List` (or do it in the TUI layer at render time) to read each file's leading 4 KiB and look for a `# ` heading. If found, `Entry.Display` uses the heading text; otherwise it stays the filename.

**Rationale:** Notes whose filename is generic (`note-2026-05-26-1400.md`) become identifiable by their content title.

**Alternatives:**

- _Frontmatter-only._ Less common in this user base; H1 is universal.
- _Don't fall back._ Filenames stay non-descriptive.

### `i` info modal

**Decision:** Reserve `i` for note-info overlay: full path, frontmatter dump, line/word counts, modified time, H1 if any.

**Rationale:** Parallel to the Dashboard's `u`, Projects' `i`, Settings' `i`. Same idiom across the TUI.

## Risks / Trade-offs

- **[Risk] Reading 4 KiB per file at list time is slow on huge projects.** → Mitigation: cache the H1 in memory keyed on `(path, mtime)`; only re-read when the file changes. If still slow, fall back to filename and load H1 lazily on cursor.
- **[Trade-off] New-note flow is opinionated about filename shape.** The dated default may not match every user's conventions. → Justified: the user can type any filename in the Huh field.

## Migration Plan

1. Implement the H1 fallback + small cache.
2. Implement the Glamour theme.
3. Implement the new-note flow + reintroduce `n` to the HelpBar.
4. Add `i` modal.
5. Regenerate `notes.txt` + add `notes_search.txt`.

Rollback: revert. The new-note files written by users stay on disk; rolling back doesn't delete user data.

## Open Questions

- Should the H1 fallback also apply on the Conversations preview-modal Glamour render? **Tentative:** Yes — same Glamour theme everywhere.
- Should the new-note flow add a `.ccmux/` marker so we don't list it? **Tentative:** No — it's a real note, list it.
