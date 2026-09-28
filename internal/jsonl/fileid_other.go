//go:build !unix

package jsonl

import "io/fs"

// fileID has no portable device/inode pair to report here; a Cursor
// then relies on size, modification time and the bytes before its
// offset to notice a replaced file.
func fileID(fs.FileInfo) (dev, ino uint64) { return 0, 0 }
