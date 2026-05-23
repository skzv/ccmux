## ADDED Requirements

### Requirement: Codex thinking mode can be changed from the TUI
The Agents TUI SHALL allow a user on the Codex sub-tab to change the persisted Codex thinking/reasoning mode using the documented keyboard control.

#### Scenario: Codex thinking mode changes successfully
- **WHEN** the user opens the Agents screen, selects the Codex sub-tab, and invokes the thinking-mode control
- **THEN** ccmux writes the next Codex reasoning-effort value to `model_reasoning_effort` in the configured Codex config file
- **THEN** the Codex sub-tab updates its displayed effort value without requiring a restart

#### Scenario: Codex thinking mode write fails
- **WHEN** the user invokes the Codex thinking-mode control and the Codex config cannot be written
- **THEN** the Codex sub-tab reports the error to the user
- **THEN** ccmux does not render a misleading saved/success state

### Requirement: Antigravity thinking mode can be changed from the TUI
The Agents TUI SHALL allow a user on the Antigravity sub-tab to change the persisted Antigravity thinking/reasoning mode using the documented keyboard control.

#### Scenario: Antigravity thinking mode changes successfully
- **WHEN** the user opens the Agents screen, selects the Antigravity sub-tab, and invokes the thinking-mode control
- **THEN** ccmux writes the next Antigravity reasoning-effort value to `reasoningEffort` in the configured Antigravity settings file
- **THEN** the Antigravity sub-tab updates its displayed effort value without requiring a restart

#### Scenario: Antigravity thinking mode write fails
- **WHEN** the user invokes the Antigravity thinking-mode control and the Antigravity settings file cannot be written
- **THEN** the Antigravity sub-tab reports the error to the user
- **THEN** ccmux does not render a misleading saved/success state

### Requirement: Thinking-mode controls are scoped to the active agent
The Agents TUI SHALL apply thinking-mode keyboard actions only to the currently active agent sub-tab.

#### Scenario: Codex control does not alter Antigravity config
- **WHEN** the user invokes the thinking-mode control while the Codex sub-tab is active
- **THEN** only the configured Codex config file is changed
- **THEN** the configured Antigravity settings file remains unchanged

#### Scenario: Antigravity control does not alter Codex config
- **WHEN** the user invokes the thinking-mode control while the Antigravity sub-tab is active
- **THEN** only the configured Antigravity settings file is changed
- **THEN** the configured Codex config file remains unchanged
