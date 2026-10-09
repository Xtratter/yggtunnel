package core

import (
	"io"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gologme/log"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/ipv6rwc"
)

// Lanes: several TCP links to the server for the tunnel's upload. The whole tunnel used to ride one TCP
// connection (the phone's link to the server); on a lossy mobile uplink one TCP halves its window at every
// loss and grows back by one segment per round trip — ~1.4 Mbit/s per second at 80 ms (03.10 diagnostics),
// so a 15 s upload test never got far. Several connections grow back several times faster.
//
// Each extra lane is a separate Yggdrasil node of the phone (own key, own link to the server): ironwood's
// encrypted sessions drop any packet whose nonce is not larger than the last one, so the packets of one
// session cannot be spread over links — but WireGuard on top keeps a replay window and takes reordering, and
// the server's WireGuard simply sees the phone come from several addresses. WireGuard datagrams go out through
// the lane with the fewest bytes still waiting for its link (ironwood patch: PendingBytes).

// laneMinSize: WireGuard datagrams at least this big (data, not ACKs) are spread over the lanes.
const laneMinSize = 400

type lane struct {
	c     *core.Core
	rwc   *ipv6rwc.ReadWriteCloser
	self  netip.Addr
	ready atomic.Bool // reached the server (an echo came back)
	extra bool        // a node of its own, stopped with the tunnel
}

func (l *lane) pending() int64 { return l.c.PendingBytes() }

// linkUp: the lane's link to the server is up.
func (l *lane) linkUp() bool {
	for _, p := range l.c.GetPeers() {
		if p.Up {
			return true
		}
	}
	return false
}

// watch keeps [ready] true only while the lane works: off as soon as its link is down (data sent into a dead
// lane is lost — every third packet with 3 lanes), on again once a ping through it reaches the server.
func (l *lane) watch(n int, server netip.Addr, pg *pinger, stop chan struct{}) {
	name := "Lane " + strconv.Itoa(n)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		up := l.linkUp()
		switch {
		case !up && l.ready.Load():
			l.ready.Store(false)
			logSink.add(name + ": link down, not used")
		case up && !l.ready.Load():
			if _, err := pg.ping(2*time.Second, func(seq uint16) error {
				_, err := l.rwc.Write(echo6(l.self, server, seq, 32))
				return err
			}); err == nil {
				l.ready.Store(true)
				logSink.add(name + " to the server is up")
			}
		}
	}
}

type lanes struct {
	mu   sync.Mutex
	all  []*lane
	next int // round-robin start for ties
	stop chan struct{}
}

// pick: the ready lane with the least waiting; ties go round the lanes. Ties are the usual case (the queues
// are mostly empty), and a lane that only gets the overflow never grows its TCP window — 0.25 sent 75 MB
// through the main node and 26 and 13 through the lanes, and the upload did not change.
func (ls *lanes) pick() *lane {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	var ready [8]*lane
	r := ready[:0]
	for _, l := range ls.all {
		if l.ready.Load() {
			r = append(r, l)
		}
	}
	if len(r) == 0 {
		return nil
	}
	ls.next++
	var best *lane
	var load int64
	for i := range r {
		l := r[(ls.next+i)%len(r)]
		if p := l.pending(); best == nil || p < load {
			best, load = l, p
		}
	}
	return best
}

func (ls *lanes) close() {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.stop != nil {
		close(ls.stop)
		ls.stop = nil
	}
	for _, l := range ls.all {
		if l.extra {
			_ = l.rwc.Close()
			l.c.Stop()
		}
	}
	ls.all = nil
}

// startLanes adds count-1 extra nodes linked to the server by [uri]; each reads its WireGuard datagrams
// into [bind] and becomes ready once a ping through it reaches [server].
func startLanes(ls *lanes, count int, uri string, server netip.Addr, port uint16, bind *yggBind, mtu uint64) {
	if count <= 1 || uri == "" {
		return
	}
	u, err := url.Parse(uri)
	if err != nil {
		logSink.add("Lanes: bad link " + uri)
		return
	}
	quiet := log.New(io.Discard, "", 0)
	for i := 1; i < count; i++ {
		c, err := core.New(config.GenerateConfig().Certificate, quiet, core.Peer{URI: u.String()})
		if err != nil {
			logSink.add("Lane " + strconv.Itoa(i+1) + ": " + err.Error())
			continue
		}
		l := &lane{c: c, rwc: ipv6rwc.NewReadWriteCloser(c), self: netip.AddrFrom16([16]byte(c.Address())), extra: true}
		// the main node's MTU: ipv6rwc defaults to 1280, the server's WireGuard datagrams are bigger, and for a
		// packet over the MTU its Read answers «packet too big» by writing from inside Read — which waits on
		// ironwood, which waits on that very Read: the lane locked up, then WireGuard, then Stop (0.28 hang report)
		l.rwc.SetMTU(mtu)
		ls.mu.Lock()
		ls.all = append(ls.all, l)
		stop := ls.stop
		ls.mu.Unlock()
		pg := newPinger()
		go func() { // the server → this lane: WireGuard to the bind, echo replies to the pinger
			buf := make([]byte, int(l.rwc.MTU())+64)
			for {
				k, err := l.rwc.Read(buf)
				if err != nil {
					return
				}
				if pg.catch(buf[:k]) {
					continue
				}
				if from, data, ok := parseUDP(buf[:k], port); ok && from.Addr() == server {
					bind.deliver(from, data)
				}
			}
		}()
		go l.watch(i+1, server, pg, stop)
	}
}
