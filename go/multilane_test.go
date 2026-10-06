package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gologme/log"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/ipv6rwc"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// The tunnel with a second lane: it comes up, and with the main node set aside WireGuard goes through the
// lane alone — the server's WireGuard takes the phone from the lane's address and answers back through it.
func TestTunnelLanes(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	quiet := log.New(io.Discard, "", 0)
	sc, err := core.New(config.GenerateConfig().Certificate, quiet, core.ListenAddress("tcp://127.0.0.1:"+strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Stop()
	srwc := ipv6rwc.NewReadWriteCloser(sc)
	saddr := netip.AddrFrom16([16]byte(sc.Address()))
	sPriv, sPub := keys(t)
	pPriv, pPub := keys(t)
	sfd, sPeer := socketpair(t)
	_ = syscall.SetNonblock(sfd, true)
	stun := &splitTun{f: os.NewFile(uintptr(sfd), "stun"), rwc: srwc, self: saddr, ipv6: true, events: make(chan tun.Event, 1)}
	stun.events <- tun.EventUp
	sbind := newYggBind(srwc, saddr, 51820)
	sdev := device.NewDevice(stun, sbind, device.NewLogger(device.LogLevelError, "server: "))
	defer sdev.Close()
	h := func(s string) string { x, _ := b64hex(s); return x }
	if err := sdev.IpcSet("private_key=" + h(sPriv) + "\npublic_key=" + h(pPub) + "\nallowed_ip=10.66.66.2/32\n"); err != nil {
		t.Fatal(err)
	}
	_ = sdev.Up()
	var fromLane atomic.Int64
	var laneAddr atomic.Value
	go func() {
		buf := make([]byte, 65535)
		for {
			k, err := srwc.Read(buf)
			if err != nil {
				return
			}
			if from, data, ok := parseUDP(buf[:k], 51820); ok {
				if a, _ := laneAddr.Load().(netip.Addr); a == from.Addr() {
					fromLane.Add(1)
				}
				sbind.deliver(from, data)
			} else if r := echoReply(buf[:k]); r != nil {
				go func() { _, _ = srwc.Write(r) }() // the real server's kernel answers pings
			}
		}
	}()
	link := "tcp://127.0.0.1:" + strconv.Itoa(port)
	pcfg, _ := GenerateConfig()
	if _, err := node.Start(pcfg, []string{link}); err != nil {
		t.Fatal(err)
	}
	defer node.Stop()
	pfd, pPeer := socketpair(t)
	if err := node.AttachTunnel(pfd, TunnelConfig{PrivateKey: pPriv, ServerKey: sPub, ServerYgg: saddr.String(), Port: 51820,
		Lanes: 2, LaneURI: link}); err != nil {
		t.Fatal(err)
	}
	ip4 := func(src, dst [4]byte, body string) []byte {
		p := make([]byte, 20+len(body))
		p[0], p[8], p[9] = 0x45, 64, 17
		binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
		copy(p[12:], src[:])
		copy(p[16:], dst[:])
		copy(p[20:], body)
		return p
	}
	var second *lane
	for deadline := time.Now().Add(20 * time.Second); second == nil; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("second lane not ready")
		}
		node.mu.Lock()
		if node.lanes != nil && len(node.lanes.all) == 2 && node.lanes.all[1].ready.Load() {
			second = node.lanes.all[1]
		}
		node.mu.Unlock()
	}
	laneAddr.Store(second.self)
	node.mu.Lock()
	node.lanes.all[0].ready.Store(false) // everything through the second lane
	node.mu.Unlock()
	up := ip4([4]byte{10, 66, 66, 2}, [4]byte{1, 1, 1, 1}, "through lane 2"+strings.Repeat(".", 500)) // data: spread over lanes
	buf := make([]byte, 2000)
	var got []byte
	for deadline := time.Now().Add(20 * time.Second); got == nil; {
		if time.Now().After(deadline) {
			t.Fatal("nothing reached the server through lane 2")
		}
		_, _ = pPeer.Write(up)
		_ = sPeer.SetReadDeadline(time.Now().Add(time.Second))
		if n, err := sPeer.Read(buf); err == nil {
			got = append([]byte(nil), buf[:n]...)
		}
	}
	if !bytes.Equal(got, up) || fromLane.Load() == 0 {
		t.Fatalf("server got %x, datagrams from the lane: %d", got, fromLane.Load())
	}
	// big, as WireGuard data from the server is: over ipv6rwc's default 1280 once wrapped (0.28 lanes locked up on it)
	down := ip4([4]byte{1, 1, 1, 1}, [4]byte{10, 66, 66, 2}, "back"+strings.Repeat(".", 1236))
	_, _ = sPeer.Write(down)
	_ = pPeer.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := pPeer.Read(buf)
	if err != nil || !bytes.Equal(buf[:n], down) {
		t.Fatalf("phone got %x, %v", buf[:n], err)
	}
	// a small datagram (like a download's ACK) stays on the main node
	before := fromLane.Load()
	small := ip4([4]byte{10, 66, 66, 2}, [4]byte{1, 1, 1, 1}, "ack")
	_, _ = pPeer.Write(small)
	_ = sPeer.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := sPeer.Read(buf); err != nil || !bytes.Equal(buf[:n], small) {
		t.Fatalf("small packet: %x %v", buf[:n], err)
	}
	if fromLane.Load() != before {
		t.Fatal("a small datagram went through the lane")
	}
	// lane 2's link dies: it is taken out, data still flows through the main node; back when the link is
	node.mu.Lock()
	node.lanes.all[0].ready.Store(true)
	node.mu.Unlock()
	lu, _ := url.Parse(link)
	if err := second.c.RemovePeer(lu, ""); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); second.ready.Load(); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("dead lane still used")
		}
	}
	data := ip4([4]byte{10, 66, 66, 2}, [4]byte{1, 1, 1, 1}, "main only"+strings.Repeat(".", 500))
	for i := 0; i < 3; i++ {
		_, _ = pPeer.Write(data)
		_ = sPeer.SetReadDeadline(time.Now().Add(5 * time.Second))
		if n, err := sPeer.Read(buf); err != nil || !bytes.Equal(buf[:n], data) {
			t.Fatalf("with lane 2 down: %x %v", buf[:n], err)
		}
	}
	if err := second.c.AddPeer(lu, ""); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(15 * time.Second); !second.ready.Load(); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("lane 2 did not come back")
		}
	}
	// the patched ironwood's counter of bytes waiting for links goes back to zero (no leak)
	for deadline := time.Now().Add(3 * time.Second); node.core.PendingBytes() != 0 || second.pending() != 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("pending bytes stuck: main %d, lane 2 %d", node.core.PendingBytes(), second.pending())
		}
	}
	if second.rwc.MTU() != node.rwc.MTU() {
		t.Fatalf("lane MTU %d, main %d", second.rwc.MTU(), node.rwc.MTU())
	}
	t.Logf("lane 2 %s: %d WireGuard datagrams reached the server from it, the answer came back", second.self, fromLane.Load())
}

// Lanes to the same address differ only by #N: Yggdrasil keeps them as separate links.
func TestLaneURIsDistinct(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	quiet := log.New(io.Discard, "", 0)
	sc, err := core.New(config.GenerateConfig().Certificate, quiet, core.ListenAddress("tcp://127.0.0.1:"+strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Stop()
	cc, err := core.New(config.GenerateConfig().Certificate, log.New(os.Stderr, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Stop()
	for _, s := range []string{"", "#2", "#3"} {
		u, _ := url.Parse("tcp://127.0.0.1:" + strconv.Itoa(port) + s)
		if err := cc.AddPeer(u, ""); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	for t0 := time.Now(); time.Since(t0) < 10*time.Second; time.Sleep(100 * time.Millisecond) {
		up := []string{}
		for _, p := range cc.GetPeers() {
			if p.Up {
				up = append(up, p.URI)
			}
		}
		if len(up) == 3 {
			t.Logf("up: %v", up)
			return
		}
	}
	t.Fatal("three lanes did not come up")
}

// echoReply answers an ICMPv6 echo request (what the server's kernel does), nil for anything else.
func echoReply(p []byte) []byte {
	if len(p) < 48 || p[0]>>4 != 6 || p[6] != 58 || p[40] != 128 {
		return nil
	}
	src, dst := netip.AddrFrom16([16]byte(p[24:40])), netip.AddrFrom16([16]byte(p[8:24]))
	icmp := append([]byte(nil), p[40:]...)
	icmp[0], icmp[2], icmp[3] = 129, 0, 0
	s, d := src.As16(), dst.As16()
	binary.BigEndian.PutUint16(icmp[2:], checksum(s[:], d[:], 58, icmp))
	return append(refIpv6Header(src, dst, 58, len(icmp)), icmp...)
}

// With empty queues the lanes take turns: each one's TCP gets traffic and grows its window.
func TestLanePickRoundRobin(t *testing.T) {
	cs := make([]struct{}, 3)
	ls := &lanes{}
	for range cs {
		c, err := core.New(config.GenerateConfig().Certificate, log.New(io.Discard, "", 0))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Stop()
		l := &lane{c: c}
		l.ready.Store(true)
		ls.all = append(ls.all, l)
	}
	ls.all[2].ready.Store(false)
	count := map[*lane]int{}
	for i := 0; i < 100; i++ {
		count[ls.pick()]++
	}
	if count[ls.all[0]] != 50 || count[ls.all[1]] != 50 || count[ls.all[2]] != 0 {
		t.Fatalf("picks %d/%d/%d", count[ls.all[0]], count[ls.all[1]], count[ls.all[2]])
	}
}
