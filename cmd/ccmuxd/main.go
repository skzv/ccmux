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
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/skzv/ccmux/internal/claude"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/tmux"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatalf("ccmuxd: %v", err)
	}
}

func run() error {
	cfg, _ := config.Load()
	if cfg.Daemon.PollIntervalSeconds == 0 {
		cfg.Daemon.PollIntervalSeconds = 2
	}
	if cfg.Daemon.IdleSecondsForNeedsInput == 0 {
		cfg.Daemon.IdleSecondsForNeedsInput = 3
	}

	srv := &server{
		cfg:      cfg,
		seen:     map[string]*tracked{},
		startedAt: time.Now(),
	}
	srv.startSleepManager()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Poll loop.
	go srv.pollLoop(ctx)

	// Unix socket listener.
	sockPath, err := daemon.SocketPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		return err
	}
	// Remove stale socket from a previous crash.
	_ = os.Remove(sockPath)
	unixLn, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen unix %q: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		return err
	}

	mux := http.NewServeMux()
	srv.routes(mux)
	httpSrv := &http.Server{Handler: mux}

	go func() {
		if err := httpSrv.Serve(unixLn); err != nil && err != http.ErrServerClosed {
			log.Printf("ccmuxd: unix serve: %v", err)
		}
	}()

	// Optional tailnet listener.
	if cfg.Daemon.ListenTailnet {
		if addr, err := tailscaleAddr(cfg.Daemon.TailnetPort); err == nil {
			go func() {
				log.Printf("ccmuxd: tailnet listening on %s", addr)
				_ = http.ListenAndServe(addr, mux)
			}()
		} else {
			log.Printf("ccmuxd: tailnet listener disabled: %v", err)
		}
	}

	log.Printf("ccmuxd: %s ready (socket %s)", version, sockPath)

	// Wait for signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("ccmuxd: shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
	return nil
}

// tracked is the per-session daemon state held across poll iterations.
type tracked struct {
	last        string    // last captured pane content (for change detection)
	lastChange  time.Time // when content last changed
	state       claude.State
	keepAwake   bool
	promptCount int
	created     time.Time
}

type server struct {
	cfg       config.Config
	startedAt time.Time
	mu        sync.Mutex
	seen      map[string]*tracked
	sleeper   *sleepManager

	// moshiState is refreshed periodically (not every poll) so we don't
	// shell out to moshi-hook every 2 seconds. When SuppressBell() is
	// true, pollOnce skips bell injection because moshi-hook is handling
	// notifications via Claude Code's hooks system.
	moshiState   moshi.Status
	moshiCheckAt time.Time
	moshiMu      sync.Mutex
}

func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	mux.HandleFunc("/v1/sessions/", s.handleSessionsItem)
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	s.mu.Lock()
	n := len(s.seen)
	s.mu.Unlock()
	writeJSON(w, daemon.HealthInfo{
		OK: true, Hostname: host, Version: version, Sessions: n, SleepMode: s.sleeper.Mode(),
	})
}

func (s *server) handleSessions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	tss, _ := tmux.List(ctx)
	out := make([]daemon.SessionState, 0, len(tss))
	for _, ts := range tss {
		t, ok := s.seen[ts.Name]
		if !ok {
			t = &tracked{created: ts.Created, state: claude.StateUnknown}
		}
		out = append(out, daemon.SessionState{
			Name: ts.Name, Host: "local", Path: ts.Path,
			Attached: ts.Attached, Windows: ts.Windows,
			Created: ts.Created, LastChange: t.lastChange,
			State: string(t.state), KeepAwake: t.keepAwake, PromptCount: t.promptCount,
		})
	}
	writeJSON(w, out)
}

func (s *server) handleSessionsItem(w http.ResponseWriter, r *http.Request) {
	// /v1/sessions/<name>[/<subaction>] — minimal stub.
	http.Error(w, "not implemented in v0.1", http.StatusNotImplemented)
}

// pollLoop is the heartbeat: capture-pane on each tmux session, derive
// state, and trigger bell when transitioning to NEEDS_INPUT.
func (s *server) pollLoop(ctx context.Context) {
	interval := time.Duration(s.cfg.Daemon.PollIntervalSeconds) * time.Second
	idleNeeds := time.Duration(s.cfg.Daemon.IdleSecondsForNeedsInput) * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pollOnce(ctx, idleNeeds)
		}
	}
}

func (s *server) pollOnce(ctx context.Context, idleNeeds time.Duration) {
	tss, err := tmux.List(ctx)
	if err != nil {
		return
	}
	suppressBell := s.moshiBellSuppressed(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	live := map[string]bool{}
	anyActive := false
	for _, ts := range tss {
		live[ts.Name] = true
		t, ok := s.seen[ts.Name]
		if !ok {
			t = &tracked{created: ts.Created, lastChange: time.Now(), state: claude.StateUnknown}
			s.seen[ts.Name] = t
		}
		pane, err := tmux.CapturePane(ctx, ts.Name, 60)
		if err != nil {
			continue
		}
		if pane != t.last {
			t.last = pane
			t.lastChange = time.Now()
		}
		newState := claude.Classify(pane, t.lastChange, idleNeeds)
		// Transition into NEEDS_INPUT triggers the bell — unless
		// moshi-hook is installed and paired, in which case it sends
		// proper structured push notifications via Claude Code hooks
		// and the bell would be a duplicate.
		if newState == claude.StateNeedsInput && t.state != claude.StateNeedsInput {
			if !suppressBell {
				_ = tmux.SendKeys(ctx, ts.Name, "\a")
			}
			t.promptCount++
		}
		t.state = newState
		if newState == claude.StateActive {
			anyActive = true
		}
	}
	// Garbage-collect tracked entries for sessions that no longer exist.
	for name := range s.seen {
		if !live[name] {
			delete(s.seen, name)
		}
	}
	// Sleep manager reacts to the boolean "any session active?".
	s.sleeper.SetActive(anyActive)
}

// moshiBellSuppressed returns true if ccmuxd should skip the BEL trigger
// because moshi-hook is handling notifications. Cached for 60s so we
// don't shell out to moshi-hook every 2-second poll.
func (s *server) moshiBellSuppressed(ctx context.Context) bool {
	s.moshiMu.Lock()
	defer s.moshiMu.Unlock()
	if time.Since(s.moshiCheckAt) > 60*time.Second {
		s.moshiState = moshi.Detect(ctx)
		s.moshiCheckAt = time.Now()
		if s.moshiState.SuppressBell() {
			log.Println("ccmuxd: moshi-hook detected — bell injection suppressed")
		}
	}
	return s.moshiState.SuppressBell()
}

// sleepManager owns the caffeinate (macOS) / systemd-inhibit (linux)
// subprocess that prevents the host from sleeping while a session is active.
// v0.1 implements Mode 1 (safe) only; Mode 2/3 land in the next ticket.
type sleepManager struct {
	mu       sync.Mutex
	holder   *exec.Cmd
	mode     string
	enabled  bool
}

func (s *server) startSleepManager() {
	s.sleeper = &sleepManager{mode: "off", enabled: true}
}

// SetActive turns the lock on/off based on session activity.
func (m *sleepManager) SetActive(active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled {
		return
	}
	if active && m.holder == nil {
		m.holder = sleepBlocker()
		if m.holder != nil {
			if err := m.holder.Start(); err == nil {
				m.mode = "safe"
				log.Printf("ccmuxd: sleep prevention engaged (%s)", m.holder.Path)
				return
			}
			m.holder = nil
		}
		m.mode = "off"
	}
	if !active && m.holder != nil {
		_ = m.holder.Process.Kill()
		_, _ = m.holder.Process.Wait()
		m.holder = nil
		m.mode = "off"
		log.Println("ccmuxd: sleep prevention released")
	}
}

func (m *sleepManager) Mode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

// sleepBlocker returns the right subprocess for this OS.
func sleepBlocker() *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("caffeinate", "-s")
	case "linux":
		return exec.Command("systemd-inhibit", "--what=sleep:idle",
			"--who=ccmuxd", "--why=Claude session active",
			"sleep", "infinity")
	default:
		return nil
	}
}

// tailscaleAddr returns "<tailscale_ip>:<port>" if Tailscale is running, else error.
func tailscaleAddr(port int) (string, error) {
	out, err := exec.Command("tailscale", "ip", "-4").Output()
	if err != nil {
		return "", err
	}
	ip := string(out)
	for _, c := range []byte{'\n', '\r', ' ', '\t'} {
		if i := indexByte(ip, c); i >= 0 {
			ip = ip[:i]
		}
	}
	if ip == "" {
		return "", fmt.Errorf("tailscale ip -4 returned empty")
	}
	return fmt.Sprintf("%s:%d", ip, port), nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
