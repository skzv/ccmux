//go:build !unix

package proctree

import "context"

// Snapshot is unsupported off Unix: tmux, and with it every pane the
// daemon reads, runs only there.
func Snapshot(context.Context) (*Table, error) {
	return nil, ErrUnsupported
}
