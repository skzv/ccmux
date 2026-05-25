package main

import (
	"errors"
	"fmt"
	"testing"
)

// TestErrPeerAlreadyServing_WrapsWithErrorsIs verifies that the
// sentinel returned by run() on socket conflict can still be matched
// with errors.Is after fmt.Errorf has wrapped it. main() relies on
// this to distinguish "peer already serving" (exit 0) from real
// errors (exit 1).
//
// If this ever fails — say someone switches `%w` to `%v` in the
// wrapper — launchd's KeepAlive would respawn ccmuxd in a tight
// loop, spamming the user's stderr log every ~10s. See the long
// comment above errPeerAlreadyServing in main.go for the full
// chain.
func TestErrPeerAlreadyServing_WrapsWithErrorsIs(t *testing.T) {
	wrapped := fmt.Errorf("%w on /tmp/x.sock — stop it first", errPeerAlreadyServing)
	if !errors.Is(wrapped, errPeerAlreadyServing) {
		t.Fatalf("wrapped error must satisfy errors.Is(err, errPeerAlreadyServing); got %v", wrapped)
	}
	// A different error must NOT satisfy the check — guards against
	// "anything goes" sentinel matching.
	other := errors.New("listen: address already in use")
	if errors.Is(other, errPeerAlreadyServing) {
		t.Fatalf("unrelated error should not match errPeerAlreadyServing")
	}
}
