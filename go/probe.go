package main

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"sync"
	"time"
)

// Pings for the connection log and the peer test. The app itself is outside the
// VPN (its peer links must not loop into it), so its pings would bypass the
// tunnel; these are built here and go exactly where the user's traffic goes:
//
//	PingYgg  — ICMPv6 echo into Yggdrasil (to the server's Yggdrasil address)
//	PingInet — ICMP echo into WireGuard (to 8.8.8.8 through the server)
//
// Replies are taken out of the packet flow before they reach the TUN.

const probeID = 0x5947 // "YG": the ICMP identifier of our echoes

type pinger struct {
	mu   sync.Mutex
	seq  uint16
	wait map[uint16]chan struct{}
}

func newPinger() *pinger { return &pinger{wait: map[uint16]chan struct{}{}} }

// pings serves the running node; each peer-test node has its own.
var pings = newPinger()

func (pg *pinger) next() (uint16, chan struct{}) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	pg.seq++
	ch := make(chan struct{}, 1)
	pg.wait[pg.seq] = ch
	return pg.seq, ch
}

func (pg *pinger) done(seq uint16) {
	pg.mu.Lock()
	delete(pg.wait, seq)
	pg.mu.Unlock()
}

// catch reports whether p is a reply to one of our echoes (then it must not go further).
func (pg *pinger) catch(p []byte) bool {
	var id, seq uint16
	switch {
	case len(p) >= 48 && p[0]>>4 == 6 && p[6] == 58 && p[40] == 129: // ICMPv6 echo reply
		id, seq = binary.BigEndian.Uint16(p[44:]), binary.BigEndian.Uint16(p[46:])
	case len(p) >= 28 && p[0]>>4 == 4 && p[9] == 1:
		h := int(p[0]&0x0f) * 4
		if len(p) < h+8 || p[h] != 0 { // ICMP echo reply
			return false
		}
		id, seq = binary.BigEndian.Uint16(p[h+4:]), binary.BigEndian.Uint16(p[h+6:])
	default:
		return false
	}
	if id != probeID {
		return false
	}
	pg.mu.Lock()
	ch := pg.wait[seq]
	pg.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return true
}

// ping sends one echo with send and waits for its reply; returns the round trip.
func (pg *pinger) ping(timeout time.Duration, send func(seq uint16) error) (time.Duration, error) {
	seq, ch := pg.next()
	defer pg.done(seq)
	t := time.Now()
	if err := send(seq); err != nil {
		return 0, err
	}
	select {
	case <-ch:
		return time.Since(t), nil
	case <-time.After(timeout):
		return 0, errors.New("timeout")
	}
}

// PingYgg pings a Yggdrasil address from the running node.
func (n *Node) PingYgg(dst string, timeout time.Duration) (time.Duration, error) {
	n.mu.Lock()
	c, rwc := n.core, n.rwc
	n.mu.Unlock()
	if c == nil || rwc == nil {
		return 0, errors.New("not running")
	}
	d, err := netip.ParseAddr(dst)
	if err != nil || !yggNet.Contains(d) {
		return 0, errors.New("not a Yggdrasil address")
	}
	self := netip.AddrFrom16([16]byte(c.Address()))
	return pings.ping(timeout, func(seq uint16) error {
		_, err := rwc.Write(echo6(self, d, seq, 32))
		return err
	})
}

// PingInet pings an IPv4 address through the WireGuard tunnel (full-tunnel mode only).
func (n *Node) PingInet(dst string, timeout time.Duration) (time.Duration, error) {
	n.mu.Lock()
	t := n.split
	n.mu.Unlock()
	if t == nil || !t.ip4.IsValid() {
		return 0, errors.New("no tunnel")
	}
	d, err := netip.ParseAddr(dst)
	if err != nil || !d.Is4() {
		return 0, errors.New("not an IPv4 address")
	}
	return pings.ping(timeout, func(seq uint16) error { return t.inject(echo4(t.ip4, d, seq)) })
}
