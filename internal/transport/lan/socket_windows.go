//go:build windows

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
		h := syscall.Handle(fd)
		// On Windows, IP_TOS is in IPPROTO_IP (level 0), SOL_SOCKET is 0xffff
		_ = syscall.SetsockoptInt(h, syscall.IPPROTO_IP, syscall.IP_TOS, 0xB8)
		_ = syscall.SetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 256*1024)
		_ = syscall.SetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_SNDBUF, 256*1024)
	})

	if err != nil {
		return err
	}
	return socketErr
}
