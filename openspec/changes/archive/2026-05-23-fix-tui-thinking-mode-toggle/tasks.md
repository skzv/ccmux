## 1. Reproduce and Pin Down Current Behavior

- [x] 1.1 Add failing TUI model tests that select the Codex sub-tab, invoke the documented thinking-mode key, and assert `model_reasoning_effort` changes under an isolated `CODEX_HOME`.
- [x] 1.2 Add failing TUI model tests that select the Antigravity sub-tab, invoke the documented thinking-mode key, and assert `reasoningEffort` changes under an isolated `ANTIGRAVITY_HOME`.
- [x] 1.3 Add scope tests proving the Codex control does not mutate Antigravity settings and the Antigravity control does not mutate Codex settings.

## 2. Fix TUI Thinking-Mode Interaction

- [x] 2.1 Fix Codex sub-tab key handling so the documented thinking-mode control writes the next known Codex effort level, reloads state after success, and reports write errors without a success message.
- [x] 2.2 Fix Antigravity sub-tab key handling so the documented thinking-mode control writes the next known Antigravity effort level, reloads state after success, and reports write errors without a success message.
- [x] 2.3 Keep parent Agents sub-tab routing scoped so thinking-mode keys only reach the active agent sub-model.

## 3. Verification

- [x] 3.1 Run focused config and TUI tests for Codex and Antigravity thinking-mode behavior.
- [x] 3.2 Run `make test` unless the change is explicitly limited by an environment issue.
- [x] 3.3 Update OpenSpec task checkboxes as implementation tasks are completed.
