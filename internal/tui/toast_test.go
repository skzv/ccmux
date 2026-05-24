package tui

import (
	"testing"
	"time"
)

// TestToast_SetActiveClear pins the basic lifecycle: Set makes the
// toast Active, Clear blanks it, ttl=0 falls back to the default.
func TestToast_SetActiveClear(t *testing.T) {
	var c toastController
	if c.Active() {
		t.Error("zero-value controller should not be Active")
	}
	c.Set(toastInfo, "hello", time.Second)
	if !c.Active() {
		t.Error("after Set, Active() should be true")
	}
	c.Clear()
	if c.Active() {
		t.Error("after Clear, Active() should be false")
	}
}

// TestToast_ErrorMinTTL — error toasts are floored at 8s even when
// the caller asks for a shorter ttl. A regression here would let
// transient error messages blink past unread.
func TestToast_ErrorMinTTL(t *testing.T) {
	var c toastController
	c.Set(toastError, "boom", 1*time.Second)
	// Just below the floor: still active.
	if !c.Active() {
		t.Fatal("error toast not Active immediately after Set")
	}
	if remaining := time.Until(c.until); remaining < 7*time.Second {
		t.Errorf("error toast TTL = %v, want >= 8s (floored)", remaining)
	}
}

// TestToast_DefaultTTL — ttl<=0 falls back to a sensible default
// (currently 3s).
func TestToast_DefaultTTL(t *testing.T) {
	var c toastController
	c.Set(toastInfo, "hello", 0)
	remaining := time.Until(c.until)
	if remaining < 2*time.Second || remaining > 4*time.Second {
		t.Errorf("default TTL = %v, want ~3s", remaining)
	}
}

// TestToast_LogRingBuffer — Set appends to the log newest-first and
// caps at toastLogSize. The help overlay reads this for "Recent
// activity".
func TestToast_LogRingBuffer(t *testing.T) {
	var c toastController
	for i := 0; i < toastLogSize+5; i++ {
		c.Set(toastInfo, "msg", time.Second)
	}
	if got := len(c.Log()); got != toastLogSize {
		t.Errorf("log length = %d, want %d (capped)", got, toastLogSize)
	}
}

// TestToast_LogNewestFirst — the most recent Set is at log[0]. The
// help overlay relies on this ordering.
func TestToast_LogNewestFirst(t *testing.T) {
	var c toastController
	c.Set(toastInfo, "first", time.Second)
	c.Set(toastInfo, "second", time.Second)
	log := c.Log()
	if len(log) < 2 {
		t.Fatalf("log too short: %v", log)
	}
	if log[0].Text != "second" {
		t.Errorf("log[0] = %q, want %q (newest-first ordering)", log[0].Text, "second")
	}
}
