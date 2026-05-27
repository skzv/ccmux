package sshsetup

import (
	"context"
	"sort"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestEnumerateUsers_Linux — Linux path: server returns
// /etc/passwd-shaped output, we filter to UID>=1000 + non-nologin
// shell, and exclude the connecting user.
func TestEnumerateUsers_Linux(t *testing.T) {
	srv := newTestServer(t)
	srv.uname = "Linux"
	srv.getentOutput = `root:x:0:0:root:/root:/bin/bash
sys:x:3:3:sys:/dev:/usr/sbin/nologin
alice:x:1000:1000:Alice:/home/alice:/bin/bash
bob:x:1001:1001:Bob:/home/bob:/bin/zsh
nobody:x:65534:65534:nobody:/:/usr/sbin/nologin
nolog:x:1002:1002:NoLog:/home/nolog:/usr/sbin/nologin
`
	withTempHome(t)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	srv.AuthorizeKey(lk.PublicLine)

	got, err := enumeratorFromTestServer(srv).Enumerate(
		context.Background(),
		Target{User: "alice", Host: srv.Host(), Port: srv.Port()},
		lk,
	)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	sort.Strings(got)
	want := []string{"bob"}
	if !equalSlices(got, want) {
		t.Errorf("EnumerateUsers = %v, want %v", got, want)
	}
}

// TestEnumerateUsers_Darwin — macOS path uses dscl; the connected
// user (alice) is filtered out; system accounts (UID<500) dropped.
func TestEnumerateUsers_Darwin(t *testing.T) {
	srv := newTestServer(t)
	srv.uname = "Darwin"
	srv.dsclOutput = `root             0
_amavisd       284
_atsserver     97
skz            501
alice          502
bob            503
`
	withTempHome(t)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	srv.AuthorizeKey(lk.PublicLine)

	got, err := enumeratorFromTestServer(srv).Enumerate(
		context.Background(),
		Target{User: "alice", Host: srv.Host(), Port: srv.Port()},
		lk,
	)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	sort.Strings(got)
	want := []string{"bob", "skz"}
	if !equalSlices(got, want) {
		t.Errorf("EnumerateUsers = %v, want %v", got, want)
	}
}

// TestEnumerateUsers_EmptyOnSingleUser — only real user is the one
// we connected as → empty result, no error. Wizard skips the
// multi-user prompt entirely on this signal.
func TestEnumerateUsers_EmptyOnSingleUser(t *testing.T) {
	srv := newTestServer(t)
	srv.uname = "Linux"
	srv.getentOutput = `root:x:0:0:root:/root:/bin/bash
alice:x:1000:1000:Alice:/home/alice:/bin/bash
`
	withTempHome(t)
	lk, err := EnsureLocalKey()
	if err != nil {
		t.Fatal(err)
	}
	srv.AuthorizeKey(lk.PublicLine)

	got, err := enumeratorFromTestServer(srv).Enumerate(
		context.Background(),
		Target{User: "alice", Host: srv.Host(), Port: srv.Port()},
		lk,
	)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("EnumerateUsers = %v, want empty", got)
	}
}

// enumeratorFromTestServer is the test seam mirror of
// installerFromTestServer — same pattern, redirects the dial to the
// in-process server's listener.
func enumeratorFromTestServer(srv *testServer) *enumerator {
	return &enumerator{
		dial: func(_ context.Context, _ string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
			cfg.Timeout = 3 * time.Second
			return srv.dialFromTest(cfg)
		},
	}
}
