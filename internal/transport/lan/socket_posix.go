//go:build !windows

package lan

import (
	"net"
	"syscall"
)

func setLowLatencySocketOpts(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}

	var socketErr error
	err = raw.Control(func(fd uintptr) {
		// 0xB8 = DSCP 46 (EF) << 2
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TOS, 0xB8)
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 256*1024)
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, 256*1024)
	})

	if err != nil {
		return err
	}
	return socketErr
}
