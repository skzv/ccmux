//go:build unix

package jsonl

import (
	"io/fs"
	"syscall"
)

// fileID returns the device and inode numbers behind fi, so a Cursor
// notices when a path now names a different file (replaced by a
// rename, deleted and recreated).
func fileID(fi fs.FileInfo) (dev, ino uint64) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), uint64(st.Ino) //nolint:unconvert // Dev and Ino are signed or narrower on some platforms
	}
	return 0, 0
}
