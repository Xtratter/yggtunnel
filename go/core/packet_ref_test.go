package core

import (
	"encoding/binary"
	"net/netip"
)

// Frozen copies of the packet code as of 0.38: the wire format reference for packet_test.go.

func refChecksum(src, dst []byte, proto byte, payload []byte) uint16 {
	var sum uint32
	add := func(b []byte) {
		for i := 0; i+1 < len(b); i += 2 {
			sum += uint32(b[i])<<8 | uint32(b[i+1])
		}
		if len(b)%2 == 1 {
			sum += uint32(b[len(b)-1]) << 8
		}
	}
	add(src)
	add(dst)
	sum += uint32(len(payload)) + uint32(proto)
	add(payload)
	for sum > 0xffff {
		sum = sum>>16 + sum&0xffff
	}
	c := ^uint16(sum)
	if c == 0 && proto == 17 {
		c = 0xffff
	}
	return c
}

func refIpv6Header(src, dst netip.Addr, proto byte, payloadLen int) []byte {
	h := make([]byte, 40, 40+payloadLen)
	h[0] = 0x60
	binary.BigEndian.PutUint16(h[4:], uint16(payloadLen))
	h[6] = proto
	h[7] = 64
	s, d := src.As16(), dst.As16()
	copy(h[8:], s[:])
	copy(h[24:], d[:])
	return h
}

func refBuildUDP(src, dst netip.AddrPort, data []byte) []byte {
	udp := make([]byte, 8+len(data))
	binary.BigEndian.PutUint16(udp[0:], src.Port())
	binary.BigEndian.PutUint16(udp[2:], dst.Port())
	binary.BigEndian.PutUint16(udp[4:], uint16(len(udp)))
	copy(udp[8:], data)
	s, d := src.Addr().As16(), dst.Addr().As16()
	binary.BigEndian.PutUint16(udp[6:], refChecksum(s[:], d[:], 17, udp))
	return append(refIpv6Header(src.Addr(), dst.Addr(), 17, len(udp)), udp...)
}

func refParseUDP(p []byte, dstPort uint16) (netip.AddrPort, []byte, bool) {
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

func refUnreachable(from netip.Addr, p []byte) []byte {
	if len(p) < 40 || p[6] == 58 { // never answer ICMPv6 with ICMPv6
		return nil
	}
	quote := p[:min(len(p), tunnelMTU-48)]
	icmp := make([]byte, 8+len(quote))
	icmp[0] = 1 // destination unreachable, code 0: no route
	copy(icmp[8:], quote)
	dst := netip.AddrFrom16([16]byte(p[8:24]))
	s, d := from.As16(), dst.As16()
	binary.BigEndian.PutUint16(icmp[2:], refChecksum(s[:], d[:], 58, icmp))
	return append(refIpv6Header(from, dst, 58, len(icmp)), icmp...)
}

func refEcho6(src, dst netip.Addr, seq uint16, size int) []byte {
	icmp := make([]byte, 8+size)
	icmp[0] = 128 // echo request
	binary.BigEndian.PutUint16(icmp[4:], probeID)
	binary.BigEndian.PutUint16(icmp[6:], seq)
	s, d := src.As16(), dst.As16()
	binary.BigEndian.PutUint16(icmp[2:], refChecksum(s[:], d[:], 58, icmp))
	return append(refIpv6Header(src, dst, 58, len(icmp)), icmp...)
}

func refSum16(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s > 0xffff {
		s = s>>16 + s&0xffff
	}
	return ^uint16(s)
}

func refEcho4(src, dst netip.Addr, seq uint16) []byte {
	p := make([]byte, 20+8+32)
	p[0], p[8], p[9] = 0x45, 64, 1
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	s, d := src.As4(), dst.As4()
	copy(p[12:], s[:])
	copy(p[16:], d[:])
	binary.BigEndian.PutUint16(p[10:], refSum16(p[:20]))
	icmp := p[20:]
	icmp[0] = 8 // echo request
	binary.BigEndian.PutUint16(icmp[4:], probeID)
	binary.BigEndian.PutUint16(icmp[6:], seq)
	binary.BigEndian.PutUint16(icmp[2:], refSum16(icmp))
	return p
}
