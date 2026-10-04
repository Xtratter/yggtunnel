package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/gologme/log"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/ipv6rwc"
)

// independent one's-complement check: header+payload sum incl. checksum must be 0xffff
func verifySum(src, dst []byte, proto byte, payload []byte) bool {
	buf := append(append(append([]byte{}, src...), dst...), 0, 0, 0, 0, 0, 0, 0, proto)
	binary.BigEndian.PutUint32(buf[32:], uint32(len(payload)))
	buf[39] = proto
	buf = append(buf, payload...)
	if len(buf)%2 == 1 {
		buf = append(buf, 0)
	}
	var s uint64
	for i := 0; i < len(buf); i += 2 {
		s += uint64(binary.BigEndian.Uint16(buf[i:]))
	}
	for s > 0xffff {
		s = s>>16 + s&0xffff
	}
	return s == 0xffff
}

func TestUDPAndICMP(t *testing.T) {
	src := netip.MustParseAddrPort("[200::1]:51820")
	dst := netip.MustParseAddrPort("[201:1234:5678::9abc]:51820")
	for _, n := range []int{0, 1, 33, 148, 1200} {
		data := bytes.Repeat([]byte{0xab, 0x01, 0xff}, n)[:n]
		p := buildUDP(src, dst, data)
		if !verifySum(p[8:24], p[24:40], 17, p[40:]) {
			t.Fatalf("bad UDP checksum, len %d", n)
		}
		from, got, ok := parseUDP(p, 51820)
		if !ok || from != src || !bytes.Equal(got, data) {
			t.Fatalf("parse: %v %v", ok, from)
		}
		if _, _, ok := parseUDP(p, 1); ok {
			t.Fatal("wrong port accepted")
		}
	}
	orig := buildUDP(netip.MustParseAddrPort("[200::1]:5000"), netip.MustParseAddrPort("[2a00::1]:443"), []byte("hello"))
	r := unreachable(netip.MustParseAddr("200::1"), orig)
	if r == nil || r[6] != 58 || r[40] != 1 || !verifySum(r[8:24], r[24:40], 58, r[40:]) {
		t.Fatal("bad ICMPv6 unreachable")
	}
	if unreachable(netip.MustParseAddr("200::1"), r) != nil {
		t.Fatal("answered ICMPv6 with ICMPv6")
	}
}

func socketpair(t *testing.T) (int, *os.File) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fds[0], os.NewFile(uintptr(fds[1]), "peer")
}

func keys(t *testing.T) (priv, pub string) {
	s, _ := WgKeyPair()
	var k map[string]string
	json.Unmarshal([]byte(s), &k)
	return k["private"], k["public"]
}

// Phone (our Node, full tunnel) ⇄ Yggdrasil ⇄ "server" (core + wireguard-go): an IPv4 packet
// written into the phone's TUN must come out of the server's TUN, and back.
func TestTunnelEndToEnd(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	// server
	scfg := config.GenerateConfig()
	logger := log.New(os.Stderr, "", 0)
	sc, err := core.New(scfg.Certificate, logger, core.ListenAddress("tcp://127.0.0.1:"+strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Stop()
	srwc := ipv6rwc.NewReadWriteCloser(sc)
	saddr := netip.MustParseAddr(net.IP(sc.Address()).String())
	sPriv, sPub := keys(t)
	pPriv, pPub := keys(t)
	sfd, sPeer := socketpair(t)
	syscall.SetNonblock(sfd, true)
	stun := &splitTun{f: os.NewFile(uintptr(sfd), "stun"), rwc: srwc, self: saddr, ipv6: true, events: make(chan tun.Event, 1)}
	stun.events <- tun.EventUp
	sbind := newYggBind(srwc, saddr, 51820)
	sdev := device.NewDevice(stun, sbind, device.NewLogger(device.LogLevelError, "server: "))
	defer sdev.Close()
	h := func(s string) string { x, _ := b64hex(s); return x }
	if err := sdev.IpcSet("private_key=" + h(sPriv) + "\npublic_key=" + h(pPub) + "\nallowed_ip=10.66.66.2/32\n"); err != nil {
		t.Fatal(err)
	}
	sdev.Up()
	go func() {
		buf := make([]byte, 65535)
		for {
			k, err := srwc.Read(buf)
			if err != nil {
				return
			}
			if from, data, ok := parseUDP(buf[:k], 51820); ok {
				sbind.deliver(from, data)
			}
		}
	}()

	// phone
	pcfg, _ := GenerateConfig()
	if _, err := node.Start(pcfg, []string{"tcp://127.0.0.1:" + strconv.Itoa(port)}); err != nil {
		t.Fatal(err)
	}
	defer node.Stop()
	pfd, pPeer := socketpair(t)
	err = node.AttachTunnel(pfd, TunnelConfig{PrivateKey: pPriv, ServerKey: sPub, ServerYgg: saddr.String(), Port: 51820, IPv6: false})
	if err != nil {
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
	up := ip4([4]byte{10, 66, 66, 2}, [4]byte{1, 1, 1, 1}, "to the internet")
	buf := make([]byte, 2000)
	var got []byte
	for deadline := time.Now().Add(20 * time.Second); got == nil; {
		if time.Now().After(deadline) {
			t.Fatal("no packet on the server side")
		}
		pPeer.Write(up) // resend until Yggdrasil has found the path and WireGuard has shaken hands
		sPeer.SetReadDeadline(time.Now().Add(time.Second))
		if n, err := sPeer.Read(buf); err == nil {
			got = buf[:n]
		}
	}
	if !bytes.Equal(got, up) {
		t.Fatalf("server got %x", got)
	}
	// and back
	down := ip4([4]byte{1, 1, 1, 1}, [4]byte{10, 66, 66, 2}, "from the internet")
	sPeer.Write(down)
	pPeer.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := pPeer.Read(buf)
	if err != nil || !bytes.Equal(buf[:n], down) {
		t.Fatalf("phone got %x, %v", buf[:n], err)
	}
	// IPv6 to the internet without server IPv6 → ICMPv6 unreachable straight back
	self := netip.MustParseAddr(net.IP(node.core.Address()).String())
	v6 := buildUDP(netip.AddrPortFrom(self, 5000), netip.MustParseAddrPort("[2a00::1]:443"), []byte("x"))
	pPeer.Write(v6)
	n, err = pPeer.Read(buf)
	if err != nil || buf[6] != 58 || buf[40] != 1 {
		t.Fatalf("expected ICMPv6 unreachable, got %x %v", buf[:n], err)
	}
	var st status
	json.Unmarshal([]byte(node.Status()), &st)
	if st.Tunnel == nil || st.Tunnel.HandshakeAgo < 0 || st.Tunnel.Rx == 0 {
		t.Fatalf("bad tunnel status %+v", st.Tunnel)
	}
}
