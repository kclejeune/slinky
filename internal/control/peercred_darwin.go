//go:build darwin

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

	var cred *unix.Xucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if err != nil {
		return fmt.Errorf("raw control: %w", err)
	}
	if credErr != nil {
		return fmt.Errorf("getsockopt LOCAL_PEERCRED: %w", credErr)
	}

	myUID := uint32(os.Getuid())
	if cred.Uid != myUID {
		return fmt.Errorf("peer UID %d does not match daemon UID %d", cred.Uid, myUID)
	}
	return nil
}
