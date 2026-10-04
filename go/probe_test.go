package main

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEchoChecksums(t *testing.T) {
	src, dst := netip.MustParseAddr("200::1"), netip.MustParseAddr("200::2")
	p := echo6(src, dst, 7, 32)
	s, d := src.As16(), dst.As16()
	if checksum(s[:], d[:], 58, p[40:]) != 0 { // a valid checksum sums to zero
		t.Fatal("bad ICMPv6 checksum")
	}
	q := echo4(netip.MustParseAddr("10.66.66.2"), netip.MustParseAddr("8.8.8.8"), 7)
	if sum16(q[:20]) != 0 || sum16(q[20:]) != 0 {
		t.Fatal("bad IPv4/ICMP checksum")
	}
}

func TestCatchReplies(t *testing.T) {
	pg := newPinger()
	seq, ch := pg.next()
	r := echo6(netip.MustParseAddr("200::2"), netip.MustParseAddr("200::1"), seq, 0)
	r[40] = 129
	if !pg.catch(r) {
		t.Fatal("ICMPv6 reply not caught")
	}
	select {
	case <-ch:
	default:
		t.Fatal("waiter not woken")
	}
	r4 := echo4(netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.66.66.2"), seq)
	r4[20] = 0
	if !pg.catch(r4) {
		t.Fatal("ICMP reply not caught")
	}
	other := echo6(netip.MustParseAddr("200::2"), netip.MustParseAddr("200::1"), 1, 0)
	other[40], other[44] = 129, 0 // someone else's id
	if pg.catch(other) {
		t.Fatal("foreign reply caught")
	}
}

// YGG_PT_PEERS="tls://… quic://…" YGG_PT_TARGET=<server Yggdrasil address> go test -run PeerTestReal -v
func TestPeerTestReal(t *testing.T) {
	peers := strings.Fields(os.Getenv("YGG_PT_PEERS"))
	if len(peers) == 0 {
		t.Skip("YGG_PT_PEERS not set")
	}
	sp, _ := strconv.Atoi(os.Getenv("YGG_PT_SPEED_PORT"))
	if err := PeerTestStart(PeerTestParams{Peers: peers, Target: os.Getenv("YGG_PT_TARGET"),
		SpeedPort: sp, SpeedToken: os.Getenv("YGG_PT_SPEED_TOKEN")}); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
		var st struct {
			Running bool              `json:"running"`
			Results []json.RawMessage `json:"results"`
		}
		_ = json.Unmarshal([]byte(PeerTestStatus()), &st)
		if !st.Running {
			for _, r := range st.Results {
				t.Log(string(r))
			}
			return
		}
	}
}

// inject wakes a Read blocked on an idle TUN and hands it the probe; normal packets still pass.
func TestSplitTunInject(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	st := &splitTun{f: r, probes: make(chan []byte, 8)}
	got := make(chan []byte, 2)
	go func() {
		for {
			bufs, sizes := [][]byte{make([]byte, 2000)}, []int{0}
			if _, err := st.Read(bufs, sizes, 0); err != nil {
				return
			}
			got <- bufs[0][:sizes[0]]
		}
	}()
	time.Sleep(200 * time.Millisecond) // Read is blocked now
	p := echo4(netip.MustParseAddr("10.66.66.2"), netip.MustParseAddr("8.8.8.8"), 1)
	if err := st.inject(p); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-got:
		if len(b) != len(p) {
			t.Fatal("wrong packet")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Read not woken")
	}
	_, _ = w.Write(p) // an ordinary IPv4 packet from the TUN goes on as before
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("normal packet lost after inject")
	}
	r.Close()
}

// The client against the real speed.sh sender run locally:
// SPEED_NO_SYSTEMD=1 SPEED_DIR=… SPEED_BIN=… SPEED_ADDR=::1 bash speed.sh; run the sender with speed.env;
// YGG_SPEED_LOCAL_TOKEN=<token> go test -run SpeedLocal -v
func TestSpeedLocal(t *testing.T) {
	tok := os.Getenv("YGG_SPEED_LOCAL_TOKEN")
	if tok == "" {
		t.Skip("YGG_SPEED_LOCAL_TOKEN not set")
	}
	c, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadBuffer(8 << 20)
	// a bottleneck like a Yggdrasil link: YGG_SPEED_LOCAL_MBIT (default 20) with a 64-packet queue, the rest dropped
	mbit, _ := strconv.Atoi(os.Getenv("YGG_SPEED_LOCAL_MBIT"))
	if mbit == 0 {
		mbit = 20
	}
	queue := make(chan []byte, 64)
	in := make(chan []byte, 8192)
	go func() {
		buf := make([]byte, 2000)
		for {
			k, err := c.Read(buf)
			if err != nil {
				return
			}
			select {
			case queue <- append([]byte(nil), buf[:k]...):
			default: // queue full: dropped
			}
		}
	}()
	go func() {
		per := time.Duration(float64(time.Second) * 1248 * 8 / float64(mbit*1_000_000))
		next := time.Now()
		for m := range queue {
			if d := time.Until(next); d > 0 {
				time.Sleep(d)
			}
			next = next.Add(per)
			if time.Until(next) < -10*time.Millisecond {
				next = time.Now()
			}
			in <- m
		}
	}()
	// "Yggdrasil" here is the loopback: the built IPv6/UDP packet's payload goes to the sender as is
	write := func(p []byte) error {
		_, err := c.WriteToUDP(p[48:], &net.UDPAddr{IP: net.IPv6loopback, Port: int(binary.BigEndian.Uint16(p[42:]))})
		return err
	}
	sp, err := parseSpeed(21446, tok, 2_000_000)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // twice: late packets of the first run must not spoil the second
		r, err := speedRun(write, in, netip.MustParseAddr("200::1"), netip.MustParseAddr("200::2"), 1, sp, nil)
		t.Logf("%+v %v", r, err)
		if err != nil || r.Kbps < float64(mbit)*1000*0.6 || r.Kbps > float64(mbit)*1000*1.3 {
			t.Fatal("bad result")
		}
	}
}
