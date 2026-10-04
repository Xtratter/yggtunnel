package main

import (
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

// Peer auto-pick: dial every configured peer, then keep only the fastest few
// (plus the server's own peer while it is up) and park the rest in reserve.
// Fewer idle TLS/QUIC links = less battery. When too few kept peers are up,
// the reserve is dialed again and the pick repeats.

var (
	pickWarmup   = 25 * time.Second // let the links come up and measure latency
	pickInterval = 20 * time.Second
)

const minUp = 2

type peerPicker struct {
	mu      sync.Mutex
	keep    int             // 0 — auto-pick off
	pinned  map[string]bool // the server's own peers (direct, wss through its site), never parked
	reserve map[string]bool // configured but parked
	stop    chan struct{}
	// serverOnly: while a pinned peer is up, every other peer is parked — the server then has only the
	// direct links to reach the phone (Yggdrasil 0.5 picks the next hop by link cost, so with public peers
	// around the server sent the phone's data the long way through them); they come back when it is down.
	serverOnly bool
	directOK   int // consecutive rounds with a pinned peer up
	// Yggdrasil reports a link's URI without its options (?key=, ?priority=), so the maps above are keyed
	// by that base form; full keeps the configured URI for dialing a parked peer again.
	full map[string]string
}

// baseURI: a peer URI without its options, as Yggdrasil reports the link.
func baseURI(uri string) string {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		return uri[:i]
	}
	return uri
}

// redial dials a parked peer again with its full URI.
func (p *peerPicker) redial(c *core.Core, base string) bool {
	uri := p.full[base]
	if uri == "" {
		uri = base
	}
	u, err := url.Parse(uri)
	return err == nil && c.AddPeer(u, "") == nil
}

var serverOnlyInterval = 5 * time.Second

type cand struct {
	uri string
	lat time.Duration
}

// startPicker runs the auto-pick loop for the node; call with n.mu held.
func (n *Node) startPicker(all []string, keep int, pinned []string, serverOnly bool) {
	pin, full := map[string]bool{}, map[string]string{}
	for _, p := range pinned {
		pin[baseURI(p)] = true
	}
	for _, u := range all {
		full[baseURI(u)] = u
	}
	serverOnly = serverOnly && len(pin) > 0
	n.picker = &peerPicker{keep: keep, pinned: pin, reserve: map[string]bool{}, stop: make(chan struct{}), serverOnly: serverOnly, full: full}
	if keep <= 0 && !serverOnly {
		return
	}
	p := n.picker
	warm, every := pickWarmup, pickInterval
	if serverOnly {
		warm, every = serverOnlyInterval, serverOnlyInterval
	}
	go func() {
		t := time.NewTimer(warm)
		defer t.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-t.C:
			}
			n.pick(all)
			t.Reset(every)
		}
	}()
}

func (n *Node) stopPicker() {
	if n.picker != nil {
		close(n.picker.stop)
		n.picker = nil
	}
}

func (n *Node) pick(all []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	c, p := n.core, n.picker
	if c == nil || p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var up []cand
	for _, info := range c.GetPeers() {
		if !info.Up || info.Inbound || p.reserve[baseURI(info.URI)] {
			continue
		}
		lat := info.Latency
		if lat <= 0 {
			lat = time.Hour // not measured yet — rank last
		}
		up = append(up, cand{baseURI(info.URI), lat})
	}
	if p.serverOnly {
		n.pickServerOnly(c, all, up)
		return
	}
	if len(up) < minUp && len(p.reserve) > 0 {
		// too few left: dial the reserve again, the next round picks anew
		for uri := range p.reserve {
			if p.redial(c, uri) {
				delete(p.reserve, uri)
			}
		}
		logSink.add("Auto-pick: only " + itoa(len(up)) + " peers up, dialing the reserve")
		return
	}
	sort.Slice(up, func(i, j int) bool { return up[i].lat < up[j].lat })
	kept := 0
	for _, x := range up {
		if p.pinned[x.uri] || kept < p.keep {
			if !p.pinned[x.uri] {
				kept++
			}
			continue
		}
		if u, err := url.Parse(x.uri); err == nil && c.RemovePeer(u, "") == nil {
			p.reserve[x.uri] = true
			logSink.add("Auto-pick: " + x.uri + " → reserve")
		}
	}
	// configured peers that are down and not kept go to reserve too, so they stop redialing
	if len(up) >= minUp {
		isUp := map[string]bool{}
		for _, x := range up {
			isUp[x.uri] = true
		}
		for _, full := range all {
			uri := baseURI(full)
			if !isUp[uri] && !p.reserve[uri] && !p.pinned[uri] {
				if u, err := url.Parse(full); err == nil && c.RemovePeer(u, "") == nil {
					p.reserve[uri] = true
				}
			}
		}
	}
}

// pickServerOnly: park every non-pinned peer once a pinned one has been up for two rounds;
// dial them all again as soon as no pinned peer is up. Call with n.mu and p.mu held.
func (n *Node) pickServerOnly(c *core.Core, all []string, up []cand) {
	p := n.picker
	direct := false
	for _, x := range up {
		direct = direct || p.pinned[x.uri]
	}
	if !direct {
		p.directOK = 0
		if len(p.reserve) > 0 {
			for uri := range p.reserve {
				if p.redial(c, uri) {
					delete(p.reserve, uri)
				}
			}
			logSink.add("Auto-pick: no direct link to the server, public peers back")
		}
		return
	}
	if p.directOK++; p.directOK < 2 {
		return
	}
	parked := 0
	for _, full := range all {
		uri := baseURI(full)
		if p.pinned[uri] || p.reserve[uri] {
			continue
		}
		if u, err := url.Parse(full); err == nil && c.RemovePeer(u, "") == nil {
			p.reserve[uri] = true
			parked++
		}
	}
	if parked > 0 {
		logSink.add("Auto-pick: direct link to the server is up, " + itoa(parked) + " public peers → reserve")
	}
}

// reserved lists parked peers; call with n.mu held.
func (n *Node) reserved() []string {
	out := []string{}
	if p := n.picker; p != nil {
		p.mu.Lock()
		for uri := range p.reserve {
			out = append(out, uri)
		}
		p.mu.Unlock()
		sort.Strings(out)
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}
