## Why

Muse Code users need the same cross-device session control, history and usage visibility as other ccmux agents. This change delivers the integration with a tested release and launch media.

## What Changes

- Add Muse Code selection, setup, command overrides, launch/resume and state detection.
- Read native versioned logs for history, previews and deduplicated token usage; safely delete complete inactive sessions.
- Update all nine website/app locales, install and validate Muse, publish v0.5.0 and draft three English X posts with screenshots and VHS clips.

## Capabilities

### New Capabilities
- `muse-code`: Session control, native data import, usage and guarded deletion.

### Modified Capabilities
None.

## Impact

Agent/config registries, conversations, usage, daemon/MCP, CLI/TUI, website/docs and release/media tooling. Optional additive usage fields preserve existing clients. Native Muse settings and credentials remain Muse-managed.
