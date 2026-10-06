package main

import (
	"bytes"
	"io"
	"math/rand"
	"net/netip"
	"testing"

	"github.com/gologme/log"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

var testLens = func() []int {
	l := []int{0, 1, 2, 3, 7, 8, 9, 39, 40, 41, 47, 48, 49, 400, 1279, 1280, 1281, 1500, 65535}
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 500; i++ {
		l = append(l, r.Intn(3000))
	}
	return l
}()

func randAddr(r *rand.Rand) netip.Addr {
	var a [16]byte
	r.Read(a[:])
	a[0] = 0x02
	return netip.AddrFrom16(a)
}

// New packet code must stay byte-identical to the 0.38 code (packet_ref_test.go): other Yggdrasil nodes see these bytes.
func TestPacketEquivalence(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, n := range testLens {
		data := make([]byte, n)
		r.Read(data)
		src, dst := randAddr(r), randAddr(r)
		s, d := src.As16(), dst.As16()
		for _, proto := range []byte{17, 58} {
			if got, want := checksum(s[:], d[:], proto, data), refChecksum(s[:], d[:], proto, data); got != want {
				t.Fatalf("checksum len %d proto %d: %04x != %04x", n, proto, got, want)
			}
		}
		if n <= 65000 {
			sp, dp := netip.AddrPortFrom(src, uint16(r.Intn(65536))), netip.AddrPortFrom(dst, uint16(r.Intn(65536)))
			got, want := buildUDP(sp, dp, data), refBuildUDP(sp, dp, data)
			if !bytes.Equal(got, want) {
				t.Fatalf("buildUDP len %d differs", n)
			}
			for _, pkt := range [][]byte{want, want[:len(want)/2], append(append([]byte{}, want...), 1, 2, 3), nil} {
				for _, port := range []uint16{dp.Port(), dp.Port() + 1} {
					a1, b1, ok1 := parseUDP(pkt, port)
					a2, b2, ok2 := refParseUDP(pkt, port)
					if ok1 != ok2 || a1 != a2 || !bytes.Equal(b1, b2) {
						t.Fatalf("parseUDP len %d differs", n)
					}
				}
			}
		}
		if g, w := unreachable(src, data), refUnreachable(src, data); !bytes.Equal(g, w) {
			t.Fatalf("unreachable len %d differs", n)
		}
		if n < 200 {
			if g, w := echo6(src, dst, uint16(n), n), refEcho6(src, dst, uint16(n), n); !bytes.Equal(g, w) {
				t.Fatalf("echo6 size %d differs", n)
			}
		}
		a4, b4 := netip.AddrFrom4([4]byte{10, 0, 0, byte(n)}), netip.AddrFrom4([4]byte{8, 8, 8, 8})
		if g, w := echo4(a4, b4, uint16(n)), refEcho4(a4, b4, uint16(n)); !bytes.Equal(g, w) {
			t.Fatalf("echo4 differs")
		}
	}
}

// a UDP checksum that comes out 0 must go on the wire as 0xffff
func TestUDPChecksumZeroIsFFFF(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	src, dst := randAddr(r), randAddr(r)
	for port := 0; port < 65536; port++ {
		sp, dp := netip.AddrPortFrom(src, 1), netip.AddrPortFrom(dst, uint16(port))
		p := buildUDP(sp, dp, []byte{0, 0})
		if p[46] == 0 && p[47] == 0 {
			t.Fatalf("zero UDP checksum on the wire")
		}
	}
}

func BenchmarkChecksum(b *testing.B) {
	data := make([]byte, 1280)
	rand.Read(data)
	var s, d [16]byte
	b.SetBytes(1280)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		checksum(s[:], d[:], 17, data)
	}
}

func BenchmarkBuildUDP(b *testing.B) {
	data := make([]byte, 1200)
	src, dst := netip.MustParseAddrPort("[200::1]:1"), netip.MustParseAddrPort("[200::2]:2")
	b.SetBytes(1200)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buildUDP(src, dst, data)
	}
}

func BenchmarkDeliver(b *testing.B) {
	bd := &yggBind{in: make(chan []byte, 256)}
	from := netip.MustParseAddrPort("[200::2]:51820")
	data := make([]byte, 1200)
	b.SetBytes(1200)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		bd.deliver(from, data)
		select {
		case <-bd.in:
		default:
		}
	}
}

func BenchmarkPick(b *testing.B) {
	ls := &lanes{}
	for i := 0; i < 3; i++ {
		c, err := core.New(config.GenerateConfig().Certificate, log.New(io.Discard, "", 0))
		if err != nil {
			b.Fatal(err)
		}
		defer c.Stop()
		l := &lane{c: c}
		l.ready.Store(true)
		ls.all = append(ls.all, l)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ls.pick()
	}
}
