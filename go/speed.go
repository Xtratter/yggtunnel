package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/netip"
	"sync"
	"time"
)

// Speed test client for speed.sh on the server (protocol 2): ask for N bytes over UDP inside Yggdrasil,
// report every 100 ms what arrived (the server sets its rate by these reports) and measure the best
// sustained 200 ms window. Request b"YTS2"+token(16)+id(4)+size(4); data b"YTD2"+id+seq+count+…;
// report b"YTA2"+id+bytes+packets+highest seq; end b"YTF2"+id+count; refusal b"YTB2"+id.

type SpeedResult struct {
	Kbps    float64 `json:"kbps"`    // the best of: a full 200 ms window, the last 40% of the data
	AvgKbps float64 `json:"avgKbps"` // the whole transfer, including the ramp-up
	Loss    float64 `json:"loss"`    // 0…1
	Bytes   int     `json:"bytes"`
	Ms      float64 `json:"ms"`
}

type speedParams struct {
	Port  uint16
	Token []byte
	Size  int
}

func parseSpeed(port int, token string, size int) (speedParams, error) {
	t, err := hex.DecodeString(token)
	if err != nil || len(t) != 16 || port <= 0 || port > 65535 {
		return speedParams{}, errors.New("speed test is not installed on the server")
	}
	if size <= 0 {
		size = 2_000_000
	}
	return speedParams{uint16(port), t, size}, nil
}

const speedWindow = 200 * time.Millisecond

// speedRun asks target for p.Size bytes from self:local; write sends an IPv6 packet into Yggdrasil,
// in brings the UDP payloads that came from target to local.
func speedRun(write func([]byte) error, in <-chan []byte, self, target netip.Addr, local uint16, p speedParams, stop <-chan struct{}) (SpeedResult, error) {
	var res SpeedResult
	id := make([]byte, 4)
	_, _ = rand.Read(id)
	req := append(append(append([]byte("YTS2"), p.Token...), id...), binary.BigEndian.AppendUint32(nil, uint32(p.Size))...)
	src, dst := netip.AddrPortFrom(self, local), netip.AddrPortFrom(target, p.Port)
	send := func(b []byte) error { return write(buildUDP(src, dst, b)) }
	got, pkts, high, count := 0, 0, -1, 0
	var first, last time.Time
	windows := map[int64]int{} // window number since first → bytes
	type arrival struct {
		t   time.Time
		sum int
	}
	var arrivals []arrival
	start := time.Now()
	tries := 0
	retry := time.NewTimer(0)
	defer retry.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	report := func() {
		b := append([]byte("YTA2"), id...)
		b = binary.BigEndian.AppendUint32(b, uint32(got))
		b = binary.BigEndian.AppendUint32(b, uint32(pkts))
		b = binary.BigEndian.AppendUint32(b, uint32(max(high, 0)))
		_ = send(b)
	}
	for done := false; !done; {
		idle := 3 * time.Second
		if got == 0 {
			idle = 20 * time.Second
		}
		select {
		case <-stop:
			return res, errors.New("stopped")
		case <-retry.C:
			if got > 0 {
				continue
			}
			if tries == 3 {
				return res, errors.New("the server's speed test does not answer (install it again to update)")
			}
			tries++
			if err := send(req); err != nil {
				return res, err
			}
			retry.Reset(2 * time.Second)
		case <-tick.C:
			if got > 0 {
				report()
			}
		case m := <-in:
			if len(m) < 8 || !bytes.Equal(m[4:8], id) {
				continue // a late packet of an earlier run
			}
			switch string(m[:4]) {
			case "YTD2":
				if len(m) < 16 {
					continue
				}
				seq, n := int(binary.BigEndian.Uint32(m[8:])), int(binary.BigEndian.Uint32(m[12:]))
				now := time.Now()
				if got == 0 {
					first = now
				}
				got += len(m)
				pkts++
				high, count, last = max(high, seq), n, now
				windows[int64(now.Sub(first)/speedWindow)] += len(m)
				arrivals = append(arrivals, arrival{now, got})
			case "YTF2":
				if len(m) >= 12 {
					count = int(binary.BigEndian.Uint32(m[8:]))
				}
				done = true
			case "YTB2":
				return res, errors.New("the server refused: busy or the daily limit is reached")
			}
		case <-time.After(idle):
			if got == 0 {
				return res, errors.New("no data from the server")
			}
			done = true
		}
	}
	res.Bytes = got
	res.Ms = float64(time.Since(start).Microseconds()) / 1000
	if count > 0 {
		res.Loss = max(0, 1-float64(pkts)/float64(count))
	}
	d := last.Sub(first)
	if got <= 1200 || d <= 0 {
		return res, errors.New("too little data arrived")
	}
	res.AvgKbps = float64(got) * 8 / 1000 / d.Seconds()
	// the best full window (the last one may be cut short); a short transfer: the average
	full := int64(d / speedWindow)
	for w, b := range windows {
		if w < full {
			res.Kbps = max(res.Kbps, float64(b)*8/1000/speedWindow.Seconds())
		}
	}
	// the tail: the last 40% of the data, sent once the rate has climbed to what the path carries
	// (2 MB is often over before a full window at the top rate)
	for _, a := range arrivals {
		if a.sum >= got*6/10 {
			if dt := last.Sub(a.t); dt > 20*time.Millisecond {
				res.Kbps = max(res.Kbps, float64(got-a.sum)*8/1000/dt.Seconds())
			}
			break
		}
	}
	if res.Kbps == 0 {
		res.Kbps = res.AvgKbps
	}
	return res, nil
}

// udpTaps: local UDP ports whose packets from Yggdrasil are taken for a speed test of the running node.
var udpTaps sync.Map // uint16 → chan []byte

// tapUDP hands an IPv6/UDP packet to a running speed test; true when it was one.
func tapUDP(p []byte) bool {
	if len(p) < 48 || p[0]>>4 != 6 || p[6] != 17 {
		return false
	}
	port := binary.BigEndian.Uint16(p[42:])
	ch, ok := udpTaps.Load(port)
	if !ok {
		return false
	}
	if _, data, ok := parseUDP(p, port); ok {
		select {
		case ch.(chan []byte) <- append([]byte(nil), data...):
		default:
		}
	}
	return true
}

func randomPort() uint16 {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return 40000 + binary.BigEndian.Uint16(b)%20000
}

// SpeedYgg measures the speed from target (the server's speed test) to the running node.
func (n *Node) SpeedYgg(target string, port int, token string, size int) (SpeedResult, error) {
	p, err := parseSpeed(port, token, size)
	if err != nil {
		return SpeedResult{}, err
	}
	n.mu.Lock()
	c, rwc := n.core, n.rwc
	n.mu.Unlock()
	if c == nil || rwc == nil {
		return SpeedResult{}, errors.New("not running")
	}
	t, err := netip.ParseAddr(target)
	if err != nil || !yggNet.Contains(t) {
		return SpeedResult{}, errors.New("not a Yggdrasil address")
	}
	local := randomPort()
	ch := make(chan []byte, 8192)
	udpTaps.Store(local, ch)
	defer udpTaps.Delete(local)
	self := netip.AddrFrom16([16]byte(c.Address()))
	return speedRun(func(b []byte) error { _, err := rwc.Write(b); return err }, ch, self, t, local, p, nil)
}
