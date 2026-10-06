package main

import (
	"context"
	"net"
	"net/netip"
	"sync"

	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/ipv6rwc"
)

// A small userspace TCP stack on top of a Yggdrasil node, for the connections the app itself makes to the
// server (SSH) while its own network path to the server's public address does not work. The tunnel's data
// path has no such stack; this one is created on the first use and goes away with the node.
//
// The stack's local address comes from the node's own /64 subnet, not from the node's main address: ipv6rwc
// accepts both as a source and a destination, and a separate address means a packet addressed to it can only
// belong to this stack — TCP of other apps on the main address is never taken (see tap).

type yggStack struct {
	rwc   *ipv6rwc.ReadWriteCloser
	net   *netstack.Net
	dev   tun.Device
	local netip.Addr
	once  sync.Once
}

// subnetAddr is ::1 in the node's /64 subnet.
func subnetAddr(c *core.Core) netip.Addr {
	var a [16]byte
	copy(a[:], address.SubnetForKey(c.PublicKey())[:])
	a[15] = 1
	return netip.AddrFrom16(a)
}

// newYggStack makes a stack with the address [local] that sends its packets with rwc. The caller feeds it
// the packets from rwc.Read with tap.
func newYggStack(rwc *ipv6rwc.ReadWriteCloser, local netip.Addr, mtu int) (*yggStack, error) {
	dev, nt, err := netstack.CreateNetTUN([]netip.Addr{local}, nil, mtu)
	if err != nil {
		return nil, err
	}
	s := &yggStack{rwc: rwc, net: nt, dev: dev, local: local}
	go func() { // the stack → Yggdrasil
		bufs, sizes := [][]byte{make([]byte, mtu+64)}, []int{0}
		for {
			if n, err := dev.Read(bufs, sizes, 0); err != nil {
				return
			} else if n > 0 && sizes[0] > 0 {
				_, _ = rwc.Write(bufs[0][:sizes[0]])
			}
		}
	}()
	return s, nil
}

// tap takes an IPv6 packet from Yggdrasil that is addressed to the stack; false — it is not ours.
func (s *yggStack) tap(p []byte) bool {
	if len(p) < 40 || p[0]>>4 != 6 || netip.AddrFrom16([16]byte(p[24:40])) != s.local {
		return false
	}
	_, _ = s.dev.Write([][]byte{p}, 0)
	return true
}

func (s *yggStack) DialContext(ctx context.Context, addr string) (net.Conn, error) {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, err
	}
	return s.net.DialContextTCPAddrPort(ctx, ap)
}

func (s *yggStack) Listen(port uint16) (net.Listener, error) {
	return s.net.ListenTCPAddrPort(netip.AddrPortFrom(s.local, port))
}

func (s *yggStack) Close() { s.once.Do(func() { _ = s.dev.Close() }) }
