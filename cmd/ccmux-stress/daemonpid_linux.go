package main

import (
	"errors"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// socketPeer connects to the unix socket at path and returns the pid
// and uid of the process holding its other end (SO_PEERCRED).
func socketPeer(path string) (pid, uid int, err error) {
	c, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		return 0, 0, err
	}
	defer c.Close()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, 0, errors.New("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var sockErr error
	if cerr := raw.Control(func(fd uintptr) {
		var cred *unix.Ucred
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if sockErr == nil {
			pid, uid = int(cred.Pid), int(cred.Uid)
		}
	}); cerr != nil {
		return 0, 0, cerr
	}
	return pid, uid, sockErr
}
