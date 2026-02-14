//go:build linux

package control

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func verifyPeer(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("not a Unix connection")
	}

	raw, err := uc.SyscallConn()
	if err != nil {
		return fmt.Errorf("getting raw conn: %w", err)
	}

	var cred *unix.Ucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return fmt.Errorf("raw control: %w", err)
	}
	if credErr != nil {
		return fmt.Errorf("getsockopt SO_PEERCRED: %w", credErr)
	}

	myUID := uint32(os.Getuid())
	if cred.Uid != myUID {
		return fmt.Errorf("peer UID %d does not match daemon UID %d", cred.Uid, myUID)
	}
	return nil
}
