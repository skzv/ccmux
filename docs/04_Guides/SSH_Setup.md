# SSH setup for remote hosts

Tailscale gets a stable `100.x.x.x` address per device; `mosh` and `ssh` still need normal key-based authentication on top. This guide explains the setup ccmux ships to make that one-shot.

## The two problems Tailscale doesn't solve

1. **Your public key isn't on the remote yet.** Without `~/.ssh/authorized_keys` containing your local public key, every attach attempt ends in `Permission denied (publickey)`.
2. **You'd have to remember `ssh-copy-id` on every new host.** Across a few machines this is friction; on a new mac mini it's a five-minute detour from whatever you actually wanted to do.

ccmux fixes both by running a one-time wizard that captures the SSH password ONCE, installs your key, and is then invisible forever after.

## Three ways to launch the wizard

### From the TUI's Network screen (proactive)

```
# inside ccmux
1. Press `7` to open the Network screen.
2. ↑/↓ to focus the remote host.
3. Press `s`.
```

The wizard opens as a centered modal. Walk through Confirm → Password → Install → (optional) Enumerate → Done. Esc cancels at any step.

### Automatically on attach failure (reactive)

The first time you press Enter on a remote session whose host doesn't trust your key yet, ccmux detects the auth failure and opens the same wizard instead of just showing the error. No extra step.

### From the CLI (scripts, muscle memory)

```bash
ccmux host setup-ssh mini             # by configured-host name
ccmux host setup-ssh alice@sputnik    # ad-hoc target
ccmux host setup-ssh alice@sputnik:2222
```

`--skip-enumerate` jumps past the multi-user prompt:

```bash
ccmux host setup-ssh --skip-enumerate mini
```

## What the wizard does, step by step

1. **Probe.** Non-interactive `ssh -o BatchMode=yes -o IdentitiesOnly=yes` checks whether key auth already works. If it does, the wizard exits with a one-line "nothing to do". If TCP refuses on port 22, you get a specific hint ("On macOS: System Settings → General → Sharing → Remote Login") instead of a generic error.
2. **Local key.** Reuses `~/.ssh/id_ed25519` (preferred) or `~/.ssh/id_rsa`. Only if neither exists does it generate a fresh passphrase-less `id_ed25519`. No passphrase keeps future attaches zero-prompt; the file is `chmod 600`.
3. **Password prompt.** Masked textinput, never logged, scrubbed after use.
4. **Install.** Connects with `golang.org/x/crypto/ssh` directly (not the system `ssh` binary), appends the public key to remote `~/.ssh/authorized_keys` with idempotent grep-based dedup, fixes perms (`chmod 700 ~/.ssh && chmod 600 ~/.ssh/authorized_keys`).
5. **Validate.** Disconnects, reconnects with key auth, runs a trivial `exit` to confirm the channel handshake works. If validation fails the install reports as broken — even though the file write succeeded — so you don't waste an attach attempt later.
6. **Enumerate (optional).** Runs `dscl . -list /Users UniqueID` (macOS) or `getent passwd` (Linux) to find other Unix accounts (UID ≥ 500 / 1000, real shells). Multi-select prompt offers each as a separate `user@host` ccmux entry.

## Trust-on-first-use for host keys

The first time you connect to a new host, ccmux writes its host key to `~/.ssh/known_hosts` automatically — same behavior as `ssh -o StrictHostKeyChecking=accept-new`.

A SUBSEQUENT mismatch (the host's key changed) is a hard failure. The wizard refuses to proceed and surfaces "⚠ host key changed for X — possible MITM. Investigate before re-adding." You can resolve this manually by removing the matching line from `~/.ssh/known_hosts` after confirming the change is expected (host reinstalled, etc.).

## What the wizard doesn't do

- **Passphrase-protected keys.** The wizard only works with keys that load without a passphrase prompt. If your `id_ed25519` requires one, the wizard fails with a clear message; load it into ssh-agent yourself and the regular ccmux attach paths will pick it up.
- **MFA / hardware-key auth.** Out of scope. If your remote requires TOTP / FIDO / etc., do the initial key setup by hand (the wizard's value is in skipping the boring case).
- **Anything to your local agent.** The installed key is just a file on disk. ssh-agent integration is up to you.

## Diagnostics

`ccmux doctor` probes every configured host and prints one-line remediation hints:

```
Configured SSH hosts:
  ✓ sputnik (alice@sputnik) — key auth ready
  ✗ flaky (bob@flaky) — key not installed; run `ccmux host setup-ssh flaky`
  ✗ mobile-mac — sshd not running. On macOS: System Settings → General → Sharing → Remote Login
  · old-server — timeout reaching old-server; is Tailscale connected on both ends?
```

A non-zero exit count from `ccmux doctor` is the number of problems found, including these.

## Treating `user@host` as separate devices

When a remote has multiple Unix accounts (a shared dev box, a build-and-test machine), ccmux models each `user@host` as a distinct device:

- `ccmux host add alice@sputnik sputnik --user alice` and `bob@sputnik` show up as separate rows in the Network screen.
- Each has its own session list, its own connection state, its own `~/.config` on the remote.
- The wizard's enumerate step is the quick way to bulk-add multiple users for a single remote.

## Where this lives in the codebase

- `internal/sshsetup/` — probe, key generation, password-bootstrap install, post-auth enumeration, TOFU host-key handling. Pure Go, uses `golang.org/x/crypto/ssh`.
- `internal/tui/sshsetup_wizard.go` — Bubble Tea model for the modal flow.
- `internal/tui/sshsetup_app_glue.go` — App-level wiring (open, complete, cancel, persist).
- `cmd/ccmux/cmd/hostssh.go` — the `host setup-ssh` CLI.
- `internal/e2e/sshsetup_e2e_test.go` — end-to-end test that drives the CLI against an in-process SSH server.
