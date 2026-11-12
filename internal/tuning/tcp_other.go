//go:build !linux

package tuning

import "syscall"

func tcpControl(_, _ string, _ syscall.RawConn) error { return nil }
