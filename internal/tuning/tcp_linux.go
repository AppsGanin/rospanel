package tuning

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

func tcpControl(_, _ string, c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(TCPUserTimeout.Milliseconds()))
	}); err != nil {
		return err
	}
	if sockErr != nil {
		return fmt.Errorf("set TCP_USER_TIMEOUT: %w", sockErr)
	}
	return nil
}
