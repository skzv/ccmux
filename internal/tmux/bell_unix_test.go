//go:build !windows

package tmux

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestWriteBellToTTY_DoesNotBlockOnFullPipe — finding: writeBellToTTY
// opened the client TTY blocking, so write(2) to a flow-stopped (^S)
// terminal blocked the daemon's poll loop forever. A full FIFO is the
// portable stand-in for a flow-stopped TTY: the write can never
// proceed. The call must return (success or error — either is fine)
// within the write deadline instead of hanging. Before the fix this
// test times out.
func TestWriteBellToTTY_DoesNotBlockOnFullPipe(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "tty")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	// Open a reader that never reads, then fill the pipe buffer via a
	// raw non-blocking fd until the kernel reports EAGAIN.
	reader, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open fifo reader: %v", err)
	}
	defer reader.Close()
	wfd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open fifo writer: %v", err)
	}
	defer syscall.Close(wfd)
	chunk := make([]byte, 4096)
	for {
		if _, err := syscall.Write(wfd, chunk); err != nil {
			break // EAGAIN: buffer is full
		}
	}

	done := make(chan error, 1)
	go func() { done <- writeBellToTTY(fifo) }()
	select {
	case <-done:
		// Returned promptly — blocked terminal can't wedge the poll loop.
	case <-time.After(5 * time.Second):
		t.Fatal("writeBellToTTY blocked on a full pipe — a flow-stopped TTY would wedge the daemon")
	}
}
