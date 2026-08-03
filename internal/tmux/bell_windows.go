//go:build windows

package tmux

import "os"

// writeBellToTTY (windows) — tmux client TTYs are /dev/* paths that
// never occur on Windows (clientTTYs filters on the /dev/ prefix), so
// this variant exists only to keep the package compiling under
// GOOS=windows. Plain blocking write; unreachable in practice.
func writeBellToTTY(tty string) error {
	f, err := os.OpenFile(tty, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write([]byte{'\a'})
	return err
}
