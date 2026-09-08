## Context

Muse 1.0.3 uses `muse resume --last` / `muse resume <UUID>` and XDG versioned JSONL logs. An offline echo probe confirmed wrapped permission transactions, metadata, runtime session/run events, committed replies and duplicate usage attribution. ccmux has agent strategies, conversation readers, usage summaries and declarative state rules.

## Goals / Non-Goals

**Goals:** Full session/history/usage/deletion support, nine-language documentation, verified v0.5.0 release and three English launch drafts with real media.
**Non-Goals:** Edit Muse credentials, infer subscription charges, publish X posts or ingest encrypted reasoning.

## Decisions

- Register muse plus muse-code alias, configurable executable and native resume commands; keep native approval defaults.
- Share a bounded, tolerant native log reader between history and usage. Deduplicate record IDs and mirrored source record IDs; count only model completion usage, not attribution copies. Preserve parent/child identities.
- Use XDG roots, UUID/date-validated session directories and native exclusive session locks for deletion. Reject active/symlink/unsupported layouts; remove only the selected session and its derived cache.
- Add optional cached/reasoning tokens and explicit cost availability to usage surfaces; preserve existing fields.
- Use isolated fixtures and real authenticated Muse sessions for acceptance; record only disposable demo content.

## Risks / Trade-offs

- Native format evolution → fixture the installed version, ignore unknown records and surface malformed supported data.
- Duplicate events → stable identity deduplication shared across fragments.
- Active-session deletion → verify native locking against a running Muse process and fail closed.
- Private content in launch media → disposable project, frame review, no login capture.

## Migration Plan

Merge passing app/site PRs; publish v0.5.0; verify renewed Homebrew token and install; deploy site with backup and live checks. Optional fields require no migration.

## Open Questions

None in product scope. User will authenticate Muse when prompted; native event/lock details are verified during implementation.
