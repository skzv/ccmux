## 1. OpenSpec

- [x] 1.1 Complete OpenSpec proposal, design, capability spec, and implementation checklist for Cursor agent support.

## 2. Agent Model

- [x] 2.1 Add Cursor to `internal/agent` with ID parsing, registry ordering, launch commands, resume args, configured command substitution, and focused unit tests.
- [x] 2.2 Extend persisted agent command config and daemon service PATH generation to include Cursor.

## 3. Product Surfaces

- [x] 3.1 Update setup wizard, doctor output, CLI help/error text, and agent install hints to include Cursor.
- [x] 3.2 Update TUI/docs/test expectations that enumerate the supported agent set.

## 4. Verification

- [x] 4.1 Run OpenSpec validation for `integrate-cursor`.
- [x] 4.2 Run focused Go tests for touched packages.
