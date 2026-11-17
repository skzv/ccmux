// Command ccmuxd is the ccmux background daemon. It polls tmux state,
// classifies Claude session state, rings the terminal bell on needs-input
// transitions, holds a sleep-prevention lock while sessions are active,
// and serves the IPC protocol over a Unix socket (and optionally a
// Tailscale-bound HTTP listener).
//
// This v0.1 entrypoint wires up the listeners and the poll loop. The
// individual subsystems (state machine, bell injector, sleep manager,
// tailnet listener) live in internal/daemon and grow file-by-file.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

var version = "dev"

// errPeerAlreadyServing is the sentinel returned by run() when another
// ccmuxd is already bound to the Unix socket. The startup-shim in main
// treats it as a clean exit (status 0) rather than a fatal error.
//
// Why this matters: the launchd plist has KeepAlive enabled. If we
// exited non-zero on this path, launchd's KeepAlive=SuccessfulExit
// would respawn us every ~10s (launchd's throttle), and each respawn
// would re-trip the same conflict, log-spamming the user. By exiting
// 0 we communicate "nothing to do, the existing daemon is serving" —
// which is the truth.
var errPeerAlreadyServing = errors.New("another ccmuxd is already serving")

func main() {
	if exit, code := handleArgs(os.Args[1:], os.Stdout, os.Stderr); exit {
		os.Exit(code)
	}
	if err := run(); err != nil {
		if errors.Is(err, errPeerAlreadyServing) {
			log.Printf("ccmuxd: %v — exiting cleanly", err)
			return
		}
		log.Fatalf("ccmuxd: %v", err)
	}
}

// applyDaemonDefaults fills in poll settings that are unset or make no
// sense. A negative poll_interval_seconds used to reach time.NewTicker,
// which panics on a non-positive interval, crash-looping the daemon —
// and so did a huge one (10000000000 overflows once multiplied by
// time.Second), hence the upper clamp.
func applyDaemonDefaults(d *config.DaemonConfig) {
	if d.PollIntervalSeconds <= 0 {
		d.PollIntervalSeconds = 2
	}
	if d.IdleSecondsForNeedsInput <= 0 {
		d.IdleSecondsForNeedsInput = 3
	}
	d.PollIntervalSeconds = min(d.PollIntervalSeconds, maxDaemonSeconds)
	d.IdleSecondsForNeedsInput = min(d.IdleSecondsForNeedsInput, maxDaemonSeconds)
}

// maxDaemonSeconds caps the poll interval and the needs-input idle
// threshold: an hour is already far past any useful value.
const maxDaemonSeconds = 3600

// loadConfig reads config.toml for the daemon. A file that can't be
// read or parsed is logged and replaced by the defaults: it used to be
// dropped silently, taking the user's sleep mode, notifications, push
// settings and projects root with it and leaving no trace why.
func loadConfig() config.Config {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("ccmuxd: ignoring config (%v); running with the default settings until it's fixed and ccmuxd restarts", err)
		cfg = config.Defaults()
	}
	return cfg
}

func run() error {
	cfg := loadConfig()
	applyDaemonDefaults(&cfg.Daemon)

	srv := newServer(cfg)
	// Unix socket listener.
	sockPath, err := daemon.SocketPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		return err
	}
	// Bind safely:
	//
	// The old logic was `os.Remove(sockPath); net.Listen(...)` — a race
	// that let two daemons start ~1s apart both succeed: the second's
	// Remove unlinked the first's socket from the filesystem, but the
	// first listener kept serving on the orphaned inode. The result is
	// the "rogue daemon" we found in the wild: same binary, no
	// requests, but accumulating heap from its background poll loop
	// because it can't be reached for `daemon stop`.
	//
	// Fix is to detect a live owner BEFORE removing. Dial the socket
	// with a short timeout. If dial succeeds, another ccmuxd is alive
	// and we refuse to start. If dial fails (no socket file, or stale
	// socket from a crash), it's safe to clean up and bind.
	// Wait briefly for any existing daemon to release the socket. During
	// a restart the previous instance is mid-graceful-shutdown when we
	// start, so a single probe would spuriously yield and leave the
	// daemon down until launchd's respawn throttle. A genuinely
	// persistent peer is still detected (waitForSocketHandoff returns
	// false after the window) and we exit cleanly.
	//
	// The probe→remove→listen sequence itself is guarded by an
	// exclusive flock on a sidecar lock file. The dial probe alone
	// leaves a TOCTOU window: two daemons racing the handoff can both
	// probe "free" and the loser's Remove unlinks the winner's
	// just-bound socket — the same rogue-daemon leak, one level up.
	// The lock is released once our listener is live (any later
	// starter's probe then correctly sees us).
	bindGuard, lockErr := acquireBindLock(sockPath+".lock", 10*time.Second)
	if lockErr != nil {
		// Another daemon is mid-bind (or wedged holding the lock) —
		// same treatment as a live peer: yield cleanly, exit 0.
		return fmt.Errorf("%w (bind lock: %v)", errPeerAlreadyServing, lockErr)
	}
	if !waitForSocketHandoff(sockPath, 3*time.Second) {
		bindGuard.release()
		// Wrap the sentinel so main() can errors.Is() to it and exit 0.
		// See errPeerAlreadyServing for why this isn't a regular error.
		return fmt.Errorf(
			"%w on %s — stop it first with `ccmux daemon stop`",
			errPeerAlreadyServing,
			sockPath,
		)
	}
	_ = os.Remove(sockPath)
	unixLn, err := net.Listen("unix", sockPath)
	if err != nil {
		bindGuard.release()
		return fmt.Errorf("listen unix %q: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		bindGuard.release()
		return err
	}
	bindGuard.release()

	// Only now that this process owns the socket do its side effects
	// start. A second ccmuxd that loses the race above must exit
	// without touching anything: it used to revert the live daemon's
	// very_dangerous sleep override, ring bells and send duplicate
	// pushes from its own poll tick before yielding.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopBackground := startBackground(srv, ctx)
	// Make sure any system-wide override (very_dangerous mode) is
	// reverted on every clean exit path. SIGKILL won't run defers; for
	// that case the launchd/systemd job re-runs the daemon, and
	// startSleepManager reverts the override the dead daemon left on.
	// stopBackground stops the loops before it releases the lock, so a
	// poll tick in flight can't re-engage it.
	defer stopBackground()

	// Unix-socket mux: full surface (tailnet-safe routes + local-only).
	mux := http.NewServeMux()
	srv.routes(mux)
	srv.localOnlyRoutes(mux)
	httpSrv := newHTTPServer(mux)

	go func() {
		if err := httpSrv.Serve(unixLn); err != nil && err != http.ErrServerClosed {
			log.Printf("ccmuxd: unix serve: %v", err)
		}
	}()

	// Optional tailnet listener. Its mux is *separate* and intentionally
	// excludes localOnlyRoutes — a tailnet peer must not be able to mint
	// pair tokens for itself.
	tailnetCtx, stopTailnet := context.WithCancel(ctx)
	var tailnetSrv *http.Server
	if cfg.Daemon.ListenTailnet {
		tailnetMux := http.NewServeMux()
		srv.routes(tailnetMux)
		tailnetSrv = newHTTPServer(rejectBrowserRequests(tailnetMux))
		go srv.serveTailnet(tailnetCtx, tailnetSrv, tailscaleAddr, tailnetRetryInterval)
	}

	log.Printf("ccmuxd: %s ready (socket %s)", version, sockPath)

	// Wait for signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("ccmuxd: shutting down")
	stopTailnet()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutCancel()
	srv.shutdownHTTP(shutCtx, tailnetSrv, httpSrv)
	return nil
}

// shutdownHTTP gracefully stops the HTTP servers (nil entries are
// skipped). The event streams are ended first: Shutdown waits for every
// in-flight handler, and an SSE handler only returns when its client
// goes away, so any connected client used to hold SIGTERM for the whole
// shutdown timeout.
func (s *server) shutdownHTTP(ctx context.Context, servers ...*http.Server) {
	s.stopEventStreams()
	for _, hs := range servers {
		if hs != nil {
			_ = hs.Shutdown(ctx)
		}
	}
}

// stopEventStreams ends every /v1/events stream, now and for any that
// open later. Idempotent.
func (s *server) stopEventStreams() {
	if s.streamsDone != nil {
		s.stopStreams.Do(func() { close(s.streamsDone) })
	}
}

// startBackground starts everything with a side effect outside this
// process — the sleep manager (which may revert a stale system sleep
// override), the tmux clipboard setup, the poll loop (bells, pushes),
// the model refresh and the Moshi detection — and returns the matching
// teardown. run() calls it only after binding the socket; a seam so
// tests can prove a daemon that loses the bind race never gets here.
var startBackground = func(srv *server, ctx context.Context) (stop func()) {
	srv.startSleepManager()

	// Best-effort: tell tmux to forward selections as OSC 52 so
	// cross-device clipboard works through SSH. Fails silently when
	// the tmux server isn't up yet. `set -s set-clipboard on` is
	// idempotent and harmless on terminals that ignore OSC 52.
	cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
	srv.clipboardApplied = srv.enableClipboard(cctx) == nil
	ccancel()

	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){
		srv.pollLoop,
		// Background model-catalog refresh. Separate goroutine so it
		// can't stall the high-frequency poll loop on a slow API call.
		srv.modelRefreshLoop,
		// Moshi detection shells out for seconds at a time; on its own
		// ticker it can't use up a poll tick's budget.
		srv.moshiLoop,
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loop(ctx)
		}()
	}
	return func() {
		// Stop the loops and wait for them before releasing the sleep
		// lock: a poll tick still in flight ends in SetActive, which
		// after Stop would re-engage the lock (and re-apply the
		// very_dangerous system override) with nothing left to undo it.
		cancel()
		if !waitGroupWithin(&wg, backgroundStopTimeout) {
			log.Printf("ccmuxd: background loops still running after %s; releasing the sleep lock anyway", backgroundStopTimeout)
		}
		srv.sleeper.Stop()
	}
}

// backgroundStopTimeout bounds how long shutdown waits for the
// background loops. They all watch the context and their shell-outs are
// cancelled with it, so this only matters when a child process ignores
// the kill. A stopped sleep manager ignores late SetActive calls anyway.
const backgroundStopTimeout = 5 * time.Second

// waitGroupWithin waits for wg, giving up after d. It reports whether
// the group finished.
func waitGroupWithin(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}
