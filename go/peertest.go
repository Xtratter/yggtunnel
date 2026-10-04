package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/gologme/log"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/ipv6rwc"
)

// Peer test: each peer is tried by a throwaway Yggdrasil node (its own fresh key)
// that has only this one peer. Measured from the phone, through that peer:
//
//	connect — until the link is up (TCP/QUIC + TLS + Yggdrasil handshake)
//	latency — the link's own round trip, as Yggdrasil measures it
//	rtt/loss — pings across the network to the target (our server), 5 tries
//	speed   — the server's speed test (speed.sh) sends SpeedBytes through this peer;
//	          only when it is installed (port and token known)
//
// Two stages: first every peer is connected and pinged (Parallel at once), then the speed is
// measured one peer at a time for the SpeedCount best by ping (a fresh node each again: keeping
// hundreds of nodes up between the stages costs too much). Without a target only connect and latency.

type PeerTestParams struct {
	Peers      []string `json:"peers"`
	Target     string   `json:"target"`     // Yggdrasil address to ping through each peer, "" — none
	Parallel   int      `json:"parallel"`   // peers connected and pinged at once
	SpeedPort  int      `json:"speedPort"`  // the server's speed test, 0 — not installed
	SpeedToken string   `json:"speedToken"` // its secret
	SpeedBytes int      `json:"speedBytes"` // per peer
	SpeedCount int      `json:"speedCount"` // the speed of this many best by ping, 0 — of all that reached the server
}

type PeerResult struct {
	URI       string  `json:"uri"`
	Done      bool    `json:"done"`
	Up        bool    `json:"up"`
	ConnectMs float64 `json:"connectMs"`
	LatencyMs float64 `json:"latencyMs"`
	RttMs     float64 `json:"rttMs"` // -1 — the target did not answer
	Loss      int     `json:"loss"`  // of 5
	Kbps      float64 `json:"kbps"`
	SpeedLoss float64 `json:"speedLoss"`
	SpeedErr  string  `json:"speedError,omitempty"`
	Error     string  `json:"error,omitempty"`
}

type peerTest struct {
	mu         sync.Mutex
	pinged     int       // peers past connect and ping
	speedTotal int       // peers picked for the speed stage (known once pinging is over)
	speedDone  int       // of them measured
	started    time.Time // of the current stage, for the time-left estimate
	running    bool
	results    []*PeerResult
	stop       chan struct{}
}

var ptest peerTest

const (
	ptConnectTimeout = 12 * time.Second
	ptRouteTimeout   = 15 * time.Second // the first ping also waits for the route to the target
	ptPings          = 5
)

// PeerTestStart begins a test in the background (one at a time).
func PeerTestStart(p PeerTestParams) error {
	ptest.mu.Lock()
	defer ptest.mu.Unlock()
	if ptest.running {
		return errors.New("already running")
	}
	if p.Parallel <= 0 {
		p.Parallel = 24
	}
	var target netip.Addr
	if p.Target != "" {
		t, err := netip.ParseAddr(p.Target)
		if err != nil || !yggNet.Contains(t) {
			return errors.New("bad target")
		}
		target = t
	}
	var speed *speedParams
	if p.SpeedPort > 0 && target.IsValid() {
		sp, err := parseSpeed(p.SpeedPort, p.SpeedToken, p.SpeedBytes)
		if err != nil {
			return err
		}
		speed = &sp
	}
	ptest.running = true
	ptest.results = make([]*PeerResult, len(p.Peers))
	for i, u := range p.Peers {
		ptest.results[i] = &PeerResult{URI: u, RttMs: -1}
	}
	stop := make(chan struct{})
	ptest.stop = stop
	results := ptest.results
	ptest.pinged, ptest.speedTotal, ptest.speedDone, ptest.started = 0, 0, 0, time.Now()
	go func() {
		defer func() {
			ptest.mu.Lock()
			ptest.running = false
			ptest.mu.Unlock()
		}()
		// stage 1: connect and ping, p.Parallel at once
		sem := make(chan struct{}, p.Parallel)
		var wg sync.WaitGroup
	feed:
		for _, r := range results {
			select {
			case <-stop:
				break feed
			case sem <- struct{}{}:
			}
			wg.Add(1)
			go func(r *PeerResult) {
				defer wg.Done()
				defer func() { <-sem }()
				testPeer(r, target, stop)
				ptest.mu.Lock()
				ptest.pinged++
				ptest.mu.Unlock()
			}(r)
		}
		wg.Wait()
		if speed == nil {
			return
		}
		// stage 2: the speed of the best by ping, one at a time — speeds measured together share the
		// phone's link and come out wrong (0.18 showed 164 Mbit/s on a 110 Mbit/s line)
		picked := speedPicks(results, p.SpeedCount)
		ptest.mu.Lock()
		ptest.speedTotal, ptest.started = len(picked), time.Now()
		ptest.mu.Unlock()
		for _, r := range picked {
			select {
			case <-stop:
				return
			default:
			}
			speedPeer(r, target, *speed, stop)
			ptest.mu.Lock()
			ptest.speedDone++
			ptest.mu.Unlock()
		}
	}()
	return nil
}

// speedPicks: the peers that reached the target, fewest losses and lowest round trip first, at most n (0 — all).
func speedPicks(results []*PeerResult, n int) []*PeerResult {
	ptest.mu.Lock()
	defer ptest.mu.Unlock()
	var ok []*PeerResult
	for _, r := range results {
		if r.Done && r.Up && r.RttMs >= 0 && r.Loss < ptPings {
			ok = append(ok, r)
		}
	}
	sort.SliceStable(ok, func(i, j int) bool {
		if ok[i].Loss != ok[j].Loss {
			return ok[i].Loss < ok[j].Loss
		}
		return ok[i].RttMs < ok[j].RttMs
	})
	if n > 0 && len(ok) > n {
		ok = ok[:n]
	}
	return ok
}

// PeerTestStop cancels the test; finished results stay.
func PeerTestStop() {
	ptest.mu.Lock()
	defer ptest.mu.Unlock()
	if ptest.running && ptest.stop != nil {
		close(ptest.stop)
		ptest.stop = nil
	}
}

// PeerTestStatus: {running, results:[…]} with finished results sorted best first.
func PeerTestStatus() string {
	ptest.mu.Lock()
	out := struct {
		Running    bool         `json:"running"`
		Total      int          `json:"total"`
		Pinged     int          `json:"pinged"`
		SpeedTotal int          `json:"speedTotal"`
		SpeedDone  int          `json:"speedDone"`
		Seconds    float64      `json:"seconds"` // since the start of the current stage
		Results    []PeerResult `json:"results"`
	}{Running: ptest.running, Total: len(ptest.results), Pinged: ptest.pinged, SpeedTotal: ptest.speedTotal, SpeedDone: ptest.speedDone,
		Seconds: time.Since(ptest.started).Seconds(), Results: []PeerResult{}}
	for _, r := range ptest.results {
		if r.Done {
			out.Results = append(out.Results, *r)
		}
	}
	ptest.mu.Unlock()
	sort.SliceStable(out.Results, func(i, j int) bool { return better(out.Results[i], out.Results[j]) })
	b, _ := json.Marshal(out)
	return string(b)
}

// better: up first; then reaching the target, fewer losses, a measured speed (faster first), lower round trip;
// else lower link latency.
func better(a, b PeerResult) bool {
	if a.Up != b.Up {
		return a.Up
	}
	if (a.RttMs >= 0) != (b.RttMs >= 0) {
		return a.RttMs >= 0
	}
	if a.RttMs >= 0 {
		if a.Loss != b.Loss {
			return a.Loss < b.Loss
		}
		if (a.Kbps > 0) != (b.Kbps > 0) {
			return a.Kbps > 0
		}
		if a.Kbps > 0 { // the real speed, when measured
			return a.Kbps > b.Kbps
		}
		return a.RttMs < b.RttMs
	}
	return a.LatencyMs+a.ConnectMs/10 < b.LatencyMs+b.ConnectMs/10
}

// ptNode: a throwaway Yggdrasil node with the one peer, and once the target is set its packet plumbing.
type ptNode struct {
	c     *core.Core
	rwc   *ipv6rwc.ReadWriteCloser
	pg    *pinger
	local uint16
	udp   chan []byte
	self  netip.Addr
}

func (n *ptNode) close() {
	if n.rwc != nil {
		n.rwc.Close()
	}
	n.c.Stop()
}

// ptConnect starts the node and waits for the link; fills Up, ConnectMs, LatencyMs or Error.
func ptConnect(uri string, res *PeerResult, stop chan struct{}) *ptNode {
	cfg := config.GenerateConfig()
	logger := log.New(io.Discard, "", 0)
	ygg := net.IPNet{IP: net.ParseIP("200::"), Mask: net.CIDRMask(7, 128)}
	t0 := time.Now()
	c, err := core.New(cfg.Certificate, logger,
		core.PeerFilter(func(ip net.IP) bool { return !ygg.Contains(ip) }), core.Peer{URI: uri})
	if err != nil {
		res.Error = err.Error()
		return nil
	}
	for {
		up := false
		for _, p := range c.GetPeers() {
			if p.Up {
				up, res.LatencyMs = true, float64(p.Latency.Microseconds())/1000
			} else if p.LastError != nil {
				res.Error = p.LastError.Error()
			}
		}
		if up {
			res.Up, res.Error = true, ""
			res.ConnectMs = float64(time.Since(t0).Microseconds()) / 1000
			return &ptNode{c: c}
		}
		if time.Since(t0) > ptConnectTimeout {
			if res.Error == "" {
				res.Error = "timeout"
			}
			c.Stop()
			return nil
		}
		select {
		case <-stop:
			res.Error = "stopped"
			c.Stop()
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// route sets up the packets to and from [target] and waits for the first answer (the route is looked up
// through this peer); false — no route in time or stopped.
func (n *ptNode) route(target netip.Addr, stop chan struct{}) (bool, string) {
	n.rwc = ipv6rwc.NewReadWriteCloser(n.c)
	n.pg = newPinger()
	n.local = randomPort()
	n.udp = make(chan []byte, 8192)
	n.self = netip.AddrFrom16([16]byte(n.c.Address()))
	rwc, pg, local, udp := n.rwc, n.pg, n.local, n.udp
	go func() {
		buf := make([]byte, 65535)
		for {
			k, err := rwc.Read(buf)
			if err != nil {
				return
			}
			if pg.catch(buf[:k]) {
				continue
			}
			if from, data, ok := parseUDP(buf[:k], local); ok && from.Addr() == target {
				select {
				case udp <- append([]byte(nil), data...):
				default:
				}
			}
		}
	}()
	for t := time.Now(); time.Since(t) < ptRouteTimeout; {
		select {
		case <-stop:
			return false, "stopped"
		default:
		}
		if _, err := n.pg.ping(time.Second, n.echo(target)); err == nil {
			return true, ""
		}
	}
	return false, "no route to the server"
}

func (n *ptNode) echo(target netip.Addr) func(uint16) error {
	return func(seq uint16) error { _, err := n.rwc.Write(echo6(n.self, target, seq, 32)); return err }
}

func (n *ptNode) latency(res *PeerResult) {
	for _, p := range n.c.GetPeers() {
		if p.Up && p.Latency > 0 {
			res.LatencyMs = float64(p.Latency.Microseconds()) / 1000
		}
	}
}

// testPeer (stage 1): connect, then ping the target through the peer.
func testPeer(r *PeerResult, target netip.Addr, stop chan struct{}) {
	res := *r
	defer func() {
		res.Done = true
		ptest.mu.Lock()
		*r = res
		ptest.mu.Unlock()
	}()
	n := ptConnect(r.URI, &res, stop)
	if n == nil {
		return
	}
	defer n.close()
	if !target.IsValid() {
		time.Sleep(time.Second) // the link's latency settles after the first exchanges
		n.latency(&res)
		return
	}
	if ok, why := n.route(target, stop); !ok {
		res.Error = why
		return
	}
	var sum time.Duration
	ok := 0
	for i := 0; i < ptPings; i++ {
		if d, err := n.pg.ping(2*time.Second, n.echo(target)); err == nil {
			sum += d
			ok++
		}
		time.Sleep(200 * time.Millisecond)
	}
	res.Loss = ptPings - ok
	n.latency(&res) // measured by now
	if ok > 0 {
		res.RttMs = float64((sum / time.Duration(ok)).Microseconds()) / 1000
	}
}

// speedPeer (stage 2): a fresh node through the same peer, the route, then the server's speed test;
// the stage-1 figures stay.
func speedPeer(r *PeerResult, target netip.Addr, speed speedParams, stop chan struct{}) {
	ptest.mu.Lock()
	res := *r
	ptest.mu.Unlock()
	defer func() {
		ptest.mu.Lock()
		r.Kbps, r.SpeedLoss, r.SpeedErr = res.Kbps, res.SpeedLoss, res.SpeedErr
		ptest.mu.Unlock()
	}()
	var tmp PeerResult
	n := ptConnect(r.URI, &tmp, stop)
	if n == nil {
		res.SpeedErr = tmp.Error
		return
	}
	defer n.close()
	if ok, why := n.route(target, stop); !ok {
		res.SpeedErr = why
		return
	}
	sr, err := speedRun(func(b []byte) error { _, err := n.rwc.Write(b); return err }, n.udp, n.self, target, n.local, speed, stop)
	if err != nil {
		res.SpeedErr = err.Error()
		return
	}
	res.Kbps, res.SpeedLoss = sr.Kbps, sr.Loss
}
