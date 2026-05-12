# Windows support

ccmux today supports Windows **through WSL2**. Native Windows support is
on the roadmap — the binary already cross-compiles for `GOOS=windows`,
but enough of the runtime (tmux, caffeinate-equivalent, launchd
equivalent) needs porting that it's not usable yet. This page tracks
what works, what doesn't, and the open TODOs.

## Recommended path: WSL2

```powershell
# In an admin PowerShell:
wsl --install
# (reboot if prompted)
```

Then inside your WSL distro (Ubuntu by default):

```bash
sudo apt update
sudo apt install -y tmux mosh git ripgrep build-essential

# Go 1.22+
sudo apt install -y golang-go        # if your distro is recent enough
# or grab from https://go.dev/dl/

# Tailscale on Linux:
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up

# Claude Code (npm-based):
curl -fsSL https://claude.ai/install.sh | bash

# Then ccmux itself:
mkdir -p ~/Projects && cd ~/Projects
git clone https://github.com/skzv/ccmux.git
cd ccmux && make bootstrap
```

From the user's perspective: this looks and behaves exactly like ccmux
on Linux, because that's what it is. WSL2 is a full Linux kernel. The
caveats:

- **Sleep prevention** is a Windows-host responsibility, not WSL's.
  ccmuxd inside WSL can't run `caffeinate` (Mac) or `systemd-inhibit`
  effectively (Windows still owns the laptop's power policy). Workaround:
  set Windows's power plan to "never sleep on AC", and Windows won't
  sleep while ccmuxd is busy in WSL.
- **Tailscale** can be installed inside WSL (recommended for tailnet
  reachability) *or* on the Windows host (then WSL routes through the
  host). Either works; the WSL-native path is simpler.
- **Mosh from your phone → WSL** works but the WSL distro needs to be
  running and its SSH port forwarded. Easiest setup: Tailscale-up inside
  WSL so your tailnet sees the WSL machine directly.
- **Moshi-hook** is macOS-only; the iOS notification path doesn't work
  from a Windows/WSL ccmuxd host today. Plain terminal BEL still fires
  and Blink/Termius will still raise a basic push.

## Native Windows: what works, what doesn't

The repo cross-compiles for `GOOS=windows GOARCH=amd64` and `GOOS=windows
GOARCH=arm64`. The TUI itself runs (Bubble Tea, Lipgloss, Bubbles all
work in Windows Terminal). But every action depends on subsystems that
have no Windows equivalent yet:

| Subsystem            | Native Windows status                                       |
|----------------------|-------------------------------------------------------------|
| Cross-compile        | ✅ both amd64 and arm64                                     |
| TUI rendering        | ✅ Bubble Tea works in Windows Terminal                     |
| Daemon (ccmuxd)      | ⚠ starts and listens, but every action errors at runtime    |
| Unix socket IPC      | ✅ Windows 10 1803+ supports AF_UNIX                         |
| tmux                 | ❌ no native Windows tmux; needs WSL or a different mux      |
| Sleep prevention     | ❌ no caffeinate; would need SetThreadExecutionState shim    |
| Battery monitor      | ❌ would need `powercfg /batteryreport` or WMI               |
| Service install      | ❌ no launchd/systemd; would need sc.exe / Task Scheduler    |
| `pgrep` (status)     | ❌ Windows fallback is `tasklist /FI "IMAGENAME eq ccmuxd.exe"` |
| OSC 52 clipboard     | ✅ Windows Terminal supports it                              |
| Mosh (iOS attach)    | ❌ no Mac/Linux mosh-server on Windows; could relay via WSL  |
| moshi-hook           | ❌ Mac-only upstream                                         |

The doctor and setup wizard now detect `runtime.GOOS == "windows"` and
print a "use WSL2" callout instead of trying to `brew install` things
that don't exist.

## Open TODOs

When a Windows VPS becomes available for testing, these are the
implementation gaps to close in priority order:

1. **Decide on the multiplexer story.** Either:
   (a) Drop a `tmux` requirement on native Windows and integrate with
       Windows Terminal panes via its JSON RPC (very different model —
       sessions don't survive disconnect), or
   (b) Bundle WSL as a hard requirement, in which case "native Windows"
       just means "install via WSL" forever.
   Most realistic short-term answer is (b). (a) would be a major
   rewrite.

2. **Sleep prevention.** Wrap `kernel32.SetThreadExecutionState` with
   `ES_CONTINUOUS|ES_SYSTEM_REQUIRED` while sessions are active.
   See `internal/sleeplock/sleeplock.go` `startLockProc()` — add a
   `case "windows":` that returns nil for the cmd and instead toggles
   the syscall directly.

3. **Battery readout.** `parseWindowsBattery()` via
   `wmic path Win32_Battery get EstimatedChargeRemaining,BatteryStatus`
   or the modern `powercfg /batteryreport /XML`. Add alongside
   `parsePmsetBatt` and `readLinuxBattery` in
   `internal/sleeplock/battery.go`.

4. **Daemon service.** New `internal/daemonservice/windows.go` that
   wraps `sc.exe create ccmuxd binPath= …`. Alternative: use Task
   Scheduler via `schtasks /Create`. Either works; SCM is more idiomatic
   but more code.

5. **Process status fallback.** `internal/daemonservice/service.go`
   line 59 uses `pgrep`. Add a Windows branch that uses `tasklist`.

6. **Service file path.** `ServicePathOrEmpty()` returns "" on
   non-mac/linux. Once the service backend exists, return the resolved
   `%APPDATA%\ccmux\ccmuxd.service.xml` or whatever shape SCM expects.

7. **Detach process flags.** Already done — see
   `cmd/ccmux/cmd/detach_windows.go`. The DETACHED_PROCESS +
   CREATE_NEW_PROCESS_GROUP combo is correct but untested against a
   real Windows ccmuxd starting from a real terminal; verify once a
   VPS is available.

8. **Doctor checks.** Replace the macOS-flavored `brew install …`
   hints in `runDoctor()` with `winget install …` equivalents when on
   native Windows. (Today doctor short-circuits with a WSL nudge so
   the hints aren't reached, but they should be there for the moment
   native support lands.)

## Testing without a VPS today

```bash
GOOS=windows GOARCH=amd64 go build ./...
GOOS=windows GOARCH=arm64 go build ./...
```

Both produce clean binaries. There's no way to exercise the runtime
behavior (tmux interactions, daemon lifecycle, service install) from
a non-Windows host. When the user provides a Windows VPS, the test
plan is:

1. Run `ccmux setup` — confirm the "use WSL2" callout fires.
2. Install WSL2 + Ubuntu, run the setup wizard inside — confirm parity
   with a Linux laptop's behavior.
3. (Future) From native Windows: run `ccmuxd.exe`, verify it listens
   on the Unix socket (`\\.\pipe\ccmux\ccmuxd.sock` or the WSL-path
   equivalent), and that `ccmux.exe list` talks to it.
