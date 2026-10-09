package netconf

import (
	"net"

	"golang.org/x/sys/unix"
)

func bindToDevice(c net.Conn, dev string) {
	if uc, ok := c.(*net.UDPConn); ok {
		if raw, err := uc.SyscallConn(); err == nil {
			raw.Control(func(fd uintptr) { unix.BindToDevice(int(fd), dev) })
		}
	}
}
