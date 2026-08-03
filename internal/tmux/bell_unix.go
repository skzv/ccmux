//go:build !windows

package tmux

import (
	"os"
	"syscall"
	"time"
)

// bellWriteTimeout bounds a single BEL write to a client TTY. A
// flow-stopped terminal (the user hit Ctrl-S, or a dead mosh/ssh
// client whose output queue filled) accepts the open but never drains;
// without a bound the daemon's poll loop would block in write(2)
// forever on that one terminal and never poll again.
const bellWriteTimeout = 2 * time.Second

// writeBellToTTY writes BEL to a client TTY without ever blocking the
// caller indefinitely. The TTY is opened O_NONBLOCK so a wedged
// terminal can't hang the open or the raw write; the write deadline
// covers the pollable-fd path, where Go's runtime would otherwise park
// the goroutine in the netpoller waiting for writability.
func writeBellToTTY(tty string) error {
	f, err := os.OpenFile(tty, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	// Best-effort: SetWriteDeadline errors on fds the runtime can't
	// poll; those keep raw O_NONBLOCK semantics (EAGAIN) instead.
	_ = f.SetWriteDeadline(time.Now().Add(bellWriteTimeout))
	_, err = f.Write([]byte{'\a'})
	return err
}
