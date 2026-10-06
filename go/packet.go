package main

import (
	"encoding/binary"
	"net/netip"
)

// Packets built and parsed by hand: IPv6/UDP for WireGuard over Yggdrasil, ICMP echoes, "no route" replies.
// The bytes are what other Yggdrasil nodes and the server see — packet_test.go pins them to the 0.38 code.

const ipv6HeaderLen = 40

// sumWords adds b to a one's-complement sum, 4 bytes at a time (a 32-bit word is hi*65536+lo ≡ hi+lo mod 65535,
// so folding to 16 bits at the end gives the same result as summing 16-bit words). b must start at an even offset
// of the summed data; an odd last byte counts as the high byte of a word.
func sumWords(sum uint64, b []byte) uint64 {
	for len(b) >= 8 {
		v := binary.BigEndian.Uint64(b)
		sum += v>>32 + v&0xffffffff
		b = b[8:]
	}
	if len(b) >= 4 {
		sum += uint64(binary.BigEndian.Uint32(b))
		b = b[4:]
	}
	if len(b) >= 2 {
		sum += uint64(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint64(b[0]) << 8
	}
	return sum
}

func foldSum(sum uint64) uint16 {
	for sum > 0xffff {
		sum = sum>>16 + sum&0xffff
	}
	return ^uint16(sum)
}

// checksum is the IPv6 pseudo-header checksum of an upper-layer payload.
func checksum(src, dst []byte, proto byte, payload []byte) uint16 {
	sum := sumWords(0, src)
	sum = sumWords(sum, dst)
	sum += uint64(len(payload)) + uint64(proto)
	c := foldSum(sumWords(sum, payload))
	if c == 0 && proto == 17 {
		c = 0xffff
	}
	return c
}

// putIPv6Header writes the 40-byte header (hop limit 64) into h.
func putIPv6Header(h []byte, src, dst *[16]byte, proto byte, payloadLen int) {
	h[0] = 0x60
	binary.BigEndian.PutUint16(h[4:], uint16(payloadLen))
	h[6] = proto
	h[7] = 64
	copy(h[8:], src[:])
	copy(h[24:], dst[:])
}

func buildUDP(src, dst netip.AddrPort, data []byte) []byte {
	n := 8 + len(data)
	p := make([]byte, ipv6HeaderLen+n)
	s, d := src.Addr().As16(), dst.Addr().As16()
	putIPv6Header(p, &s, &d, 17, n)
	udp := p[ipv6HeaderLen:]
	binary.BigEndian.PutUint16(udp[0:], src.Port())
	binary.BigEndian.PutUint16(udp[2:], dst.Port())
	binary.BigEndian.PutUint16(udp[4:], uint16(n))
	copy(udp[8:], data)
	binary.BigEndian.PutUint16(udp[6:], checksum(s[:], d[:], 17, udp))
	return p
}

// parseUDP returns the source and payload of an IPv6/UDP packet sent to dstPort.
func parseUDP(p []byte, dstPort uint16) (netip.AddrPort, []byte, bool) {
	if len(p) < 48 || p[0]>>4 != 6 || p[6] != 17 {
		return netip.AddrPort{}, nil, false
	}
	n := int(binary.BigEndian.Uint16(p[4:]))
	if n < 8 || 40+n > len(p) || binary.BigEndian.Uint16(p[42:]) != dstPort {
		return netip.AddrPort{}, nil, false
	}
	src := netip.AddrFrom16([16]byte(p[8:24]))
	return netip.AddrPortFrom(src, binary.BigEndian.Uint16(p[40:])), p[48 : 40+n], true
}

// unreachable makes an ICMPv6 "no route" reply to an IPv6 packet, so apps give up
// on IPv6 at once and use IPv4 when the server has no IPv6 internet.
func unreachable(from netip.Addr, p []byte) []byte {
	if len(p) < 40 || p[6] == 58 { // never answer ICMPv6 with ICMPv6
		return nil
	}
	quote := p[:min(len(p), tunnelMTU-48)]
	n := 8 + len(quote)
	out := make([]byte, ipv6HeaderLen+n)
	s, d := from.As16(), [16]byte(p[8:24])
	putIPv6Header(out, &s, &d, 58, n)
	icmp := out[ipv6HeaderLen:]
	icmp[0] = 1 // destination unreachable, code 0: no route
	copy(icmp[8:], quote)
	binary.BigEndian.PutUint16(icmp[2:], checksum(s[:], d[:], 58, icmp))
	return out
}

func echo6(src, dst netip.Addr, seq uint16, size int) []byte {
	n := 8 + size
	p := make([]byte, ipv6HeaderLen+n)
	s, d := src.As16(), dst.As16()
	putIPv6Header(p, &s, &d, 58, n)
	icmp := p[ipv6HeaderLen:]
	icmp[0] = 128 // echo request
	binary.BigEndian.PutUint16(icmp[4:], probeID)
	binary.BigEndian.PutUint16(icmp[6:], seq)
	binary.BigEndian.PutUint16(icmp[2:], checksum(s[:], d[:], 58, icmp))
	return p
}

// sum16 is the plain Internet checksum (IPv4 header, ICMP).
func sum16(b []byte) uint16 { return foldSum(sumWords(0, b)) }

func echo4(src, dst netip.Addr, seq uint16) []byte {
	p := make([]byte, 20+8+32)
	p[0], p[8], p[9] = 0x45, 64, 1
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	s, d := src.As4(), dst.As4()
	copy(p[12:], s[:])
	copy(p[16:], d[:])
	binary.BigEndian.PutUint16(p[10:], sum16(p[:20]))
	icmp := p[20:]
	icmp[0] = 8 // echo request
	binary.BigEndian.PutUint16(icmp[4:], probeID)
	binary.BigEndian.PutUint16(icmp[6:], seq)
	binary.BigEndian.PutUint16(icmp[2:], sum16(icmp))
	return p
}
