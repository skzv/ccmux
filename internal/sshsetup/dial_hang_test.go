package sshsetup

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestDefaultSSHDial_CancelUnblocksSilentServer — a server that accepts
// the TCP connection and never sends an SSH banner used to hang the
// handshake forever; cancelling the wizard must stop it.
func TestDefaultSSHDial_CancelUnblocksSilentServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // hold the connection open, say nothing
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	cfg := &ssh.ClientConfig{
		User:            "x",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Minute, // longer than the test: only ctx can end it
	}
	done := make(chan error, 1)
	go func() {
		_, err := defaultSSHDial(ctx, ln.Addr().String(), cfg)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handshake with a silent server did not stop on cancel")
	}
}
