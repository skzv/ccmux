package tui

import (
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Debug logging is opt-in via CCMUX_DEBUG=1. The TUI uses Bubble Tea's
// alt-screen which means stdout/stderr are taken; we route through a
// dedicated file so log calls don't corrupt the rendering.
//
// File: ~/.local/state/ccmux/ccmux.log (truncated each run).

var (
	debugMu     sync.Mutex
	debugFH     *os.File
	debugLog    *log.Logger
	debugInited bool
)

// initDebugLog opens the debug log file when CCMUX_DEBUG is set. Safe
// to call repeatedly — only the first call has an effect.
func initDebugLog() {
	debugMu.Lock()
	defer debugMu.Unlock()
	if debugInited {
		return
	}
	debugInited = true
	if os.Getenv("CCMUX_DEBUG") != "1" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".local", "state", "ccmux")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "ccmux.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	debugFH = f
	debugLog = log.New(f, "", log.Ltime|log.Lmicroseconds|log.Lshortfile)
	debugLog.Printf("ccmux debug log opened (CCMUX_DEBUG=1)")
}

// debugLogger returns the active *log.Logger, or nil when CCMUX_DEBUG
// is unset. Callers should guard:
//
//	if dbg := debugLogger(); dbg != nil { dbg.Printf("…") }
func debugLogger() *log.Logger {
	debugMu.Lock()
	defer debugMu.Unlock()
	return debugLog
}

// closeDebugLog closes the file. Called from the binary on shutdown.
func closeDebugLog() {
	debugMu.Lock()
	defer debugMu.Unlock()
	if debugFH != nil {
		_ = debugFH.Close()
		debugFH = nil
		debugLog = nil
	}
}
