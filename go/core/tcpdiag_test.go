package core

import (
	"context"
	"net"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// A TCP connection of this process shows up with the kernel's TCP_INFO; the CC can be switched to reno.
func TestOwnTCPSockets(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// loopback is skipped, so connect out only if the network allows; else just check loopback is ignored
	for _, s := range ownTCPSockets() {
		if s.remote.Addr().IsLoopback() {
			t.Fatalf("loopback listed: %v", s.remote)
		}
	}
	c, err := net.Dial("tcp", "1.1.1.1:443")
	if err != nil {
		t.Skip("no network:", err)
	}
	defer c.Close()
	var found *tcpSock
	for _, s := range ownTCPSockets() {
		if s.remote.String() == "1.1.1.1:443" {
			found = &s
		}
	}
	if found == nil {
		t.Fatal("own socket not found")
	}
	t.Logf("cc=%s cwnd=%d ssthresh=%d rtt=%dus mss=%d", found.cc, found.info.Snd_cwnd, found.info.Snd_ssthresh, found.info.Rtt, found.info.Snd_mss)
	if n, err := DiagSetCC("reno"); err != nil || n == 0 {
		t.Fatalf("set reno: %d %v", n, err)
	}
	for _, s := range ownTCPSockets() {
		if s.remote.String() == "1.1.1.1:443" && !strings.HasPrefix(s.cc, "reno") {
			t.Fatalf("cc still %q", s.cc)
		}
	}
}

// The unsent limit lands on a live socket and can be lifted again.
func TestLinkLowat(t *testing.T) {
	c, err := net.Dial("tcp", "1.1.1.1:443")
	if err != nil {
		t.Skip("no network:", err)
	}
	defer c.Close()
	fd := -1
	for _, s := range ownTCPSockets() {
		if s.remote.String() == "1.1.1.1:443" {
			fd = s.fd
		}
	}
	if fd < 0 {
		t.Fatal("socket not found")
	}
	get := func() int { v, _ := unix.GetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT); return v }
	t.Logf("before: %d", get())
	SetLinkLowat(LinkLowatDefault)
	if get() != LinkLowatDefault {
		t.Fatalf("lowat %d", get())
	}
	SetLinkLowat(0)
	if get() != 0 {
		t.Fatalf("lowat not lifted: %d", get())
	}
}

// With the limit the kernel stops taking data much earlier (the writer waits) — the point of it.
func TestLowatBackpressure(t *testing.T) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) { _ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, 4096) })
	}}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
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
			defer c.Close() // never read: the window fills and the rest stays unsent
		}
	}()
	taken := func(lowat int) int {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		raw, _ := c.(*net.TCPConn).SyscallConn()
		total := 0
		_ = raw.Control(func(fd uintptr) {
			_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, lowat)
			buf := make([]byte, 16<<10)
			for i := 0; i < 2000; i++ {
				n, err := unix.Write(int(fd), buf)
				if err != nil {
					break
				}
				total += n
			}
			if in, err := unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO); err == nil {
				t.Logf("lowat %d: unsent %d KB", lowat, in.Notsent_bytes>>10)
			}
		})
		return total
	}
	free, limited := taken(0), taken(32<<10)
	t.Logf("taken without a limit %d KB, with 32 KB limit %d KB", free>>10, limited>>10)
	if limited >= free {
		t.Fatal("the limit does not hold the writer back")
	}
}

// Two connections to the same address (lanes to one server) are told apart by their local ports.
func TestOwnTCPSocketsLocalPorts(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	includeLoopback = true
	defer func() { includeLoopback = false }()
	a, _ := net.Dial("tcp", ln.Addr().String())
	b, _ := net.Dial("tcp", ln.Addr().String())
	defer a.Close()
	defer b.Close()
	ports := map[uint16]bool{}
	for _, s := range ownTCPSockets() {
		if s.remote.String() == ln.Addr().String() {
			ports[s.local] = true
		}
	}
	if len(ports) != 2 || ports[0] {
		t.Fatalf("local ports %v", ports)
	}
}
