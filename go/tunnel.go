package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/yggdrasil-network/yggdrasil-go/src/ipv6rwc"
)

// Full tunnel: WireGuard to the user's server, with WireGuard's own UDP carried
// inside Yggdrasil. No extra network stack: the UDP/IPv6 packets are built and
// parsed here and go straight through ipv6rwc.
//
//	apps → TUN ─┬─ 200::/7 ───────────────▶ Yggdrasil (as in Yggdrasil-only mode)
//	            └─ everything else → WireGuard → UDP/IPv6 → Yggdrasil → server

const tunnelMTU = 1280

// TunnelConfig comes from the app as JSON.
type TunnelConfig struct {
	PrivateKey string `json:"privateKey"` // phone, base64
	ServerKey  string `json:"serverKey"`  // server, base64
	ServerYgg  string `json:"serverYgg"`  // server's Yggdrasil address
	Port       int    `json:"port"`       // server's WireGuard port
	IPv6       bool   `json:"ipv6"`       // the server has IPv6 internet
	ClientIP4  string `json:"clientIp4"`  // the phone's address in the tunnel (for PingInet)
	Lanes      int    `json:"lanes"`      // links to the server for the tunnel (lanes.go), 1 — just the node's own
	LaneURI    string `json:"laneUri"`    // the server's direct link the extra lanes dial
}

var yggNet = netip.MustParsePrefix("200::/7")

// ---- conn.Bind: WireGuard's "UDP socket" on top of Yggdrasil -----------------

type yggEndpoint struct{ ap netip.AddrPort }

func (e yggEndpoint) ClearSrc()           {}
func (e yggEndpoint) SrcToString() string { return "" }
func (e yggEndpoint) DstToString() string { return e.ap.String() }
func (e yggEndpoint) DstToBytes() []byte  { b, _ := e.ap.MarshalBinary(); return b }
func (e yggEndpoint) DstIP() netip.Addr   { return e.ap.Addr() }
func (e yggEndpoint) SrcIP() netip.Addr   { return netip.Addr{} }

type yggBind struct {
	rwc   *ipv6rwc.ReadWriteCloser
	local netip.AddrPort
	lanes *lanes // nil — only rwc
	in    chan []byte
	mu    sync.Mutex
	done  chan struct{}
}

func newYggBind(rwc *ipv6rwc.ReadWriteCloser, self netip.Addr, port uint16) *yggBind {
	return &yggBind{rwc: rwc, local: netip.AddrPortFrom(self, port), in: make(chan []byte, 256)}
}

// withLanes: send through the least loaded of these nodes (lanes.go) instead of the one rwc.
func (b *yggBind) withLanes(ls *lanes) *yggBind { b.lanes = ls; return b }

// deliver hands a WireGuard datagram from Yggdrasil to the bind (dropped if WireGuard lags).
func (b *yggBind) deliver(from netip.AddrPort, data []byte) {
	m := make([]byte, 18+len(data)) // the sender's address and port, then the datagram (Open's receive func parses it)
	a := from.Addr().As16()
	copy(m, a[:])
	binary.BigEndian.PutUint16(m[16:], from.Port())
	copy(m[18:], data)
	select {
	case b.in <- m:
	default:
		bindDrops.Add(1)
	}
}

func (b *yggBind) Open(uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done != nil {
		return nil, 0, conn.ErrBindAlreadyOpen
	}
	done := make(chan struct{})
	b.done = done
	recv := func(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		select {
		case <-done:
			return 0, net.ErrClosed
		case m := <-b.in:
			from := netip.AddrPortFrom(netip.AddrFrom16([16]byte(m[:16])), binary.BigEndian.Uint16(m[16:18]))
			sizes[0] = copy(packets[0], m[18:])
			eps[0] = yggEndpoint{from}
			return 1, nil
		}
	}
	return []conn.ReceiveFunc{recv}, b.local.Port(), nil
}

func (b *yggBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done != nil {
		close(b.done)
		b.done = nil
	}
	return nil
}

func (b *yggBind) SetMark(uint32) error { return nil }
func (b *yggBind) BatchSize() int       { return 1 }

func (b *yggBind) Send(bufs [][]byte, ep conn.Endpoint) error {
	e, ok := ep.(yggEndpoint)
	if !ok {
		return conn.ErrWrongEndpointType
	}
	for _, buf := range bufs {
		rwc, src := b.rwc, b.local
		// only data goes round the lanes; small datagrams (the ACKs of a download, keepalives) stay on the
		// main node — the server's WireGuard answers the address it heard last, and with ACKs from every lane
		// a download hopped between them and fell from 90–128 to 36–46 Mbit/s (0.26)
		if b.lanes != nil && len(buf) >= laneMinSize {
			if l := b.lanes.pick(); l != nil {
				rwc, src = l.rwc, netip.AddrPortFrom(l.self, b.local.Port())
			}
		}
		if _, err := rwc.Write(buildUDP(src, e.ap, buf)); err != nil {
			return err
		}
	}
	return nil
}

func (b *yggBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	if !yggNet.Contains(ap.Addr()) {
		return nil, fmt.Errorf("%s is not a Yggdrasil address", ap.Addr())
	}
	return yggEndpoint{ap}, nil
}

// ---- tun.Device: the Android TUN, with Yggdrasil traffic split off -----------

type splitTun struct {
	f      *os.File
	rwc    *ipv6rwc.ReadWriteCloser
	self   netip.Addr
	ipv6   bool
	ip4    netip.Addr  // the phone's tunnel address
	probes chan []byte // our own pings, sent as if an app had sent them
	events chan tun.Event
	once   sync.Once
}

// inject queues a packet for WireGuard and wakes a Read blocked on the TUN.
func (t *splitTun) inject(p []byte) error {
	select {
	case t.probes <- p:
	default:
		return errors.New("busy")
	}
	return t.f.SetReadDeadline(time.Now())
}

func (t *splitTun) File() *os.File           { return t.f }
func (t *splitTun) MTU() (int, error)        { return tunnelMTU, nil }
func (t *splitTun) Name() (string, error)    { return "yggtunnel", nil }
func (t *splitTun) Events() <-chan tun.Event { return t.events }
func (t *splitTun) BatchSize() int           { return 1 }

func (t *splitTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	for {
		select {
		case p := <-t.probes:
			sizes[0] = copy(bufs[0][offset:], p)
			return 1, nil
		default:
		}
		n, err := t.f.Read(bufs[0][offset:])
		if errors.Is(err, os.ErrDeadlineExceeded) { // woken by inject
			_ = t.f.SetReadDeadline(time.Time{})
			continue
		}
		if err != nil {
			return 0, err
		}
		p := bufs[0][offset : offset+n]
		if n >= 40 && p[0]>>4 == 6 {
			dst := netip.AddrFrom16([16]byte(p[24:40]))
			if yggNet.Contains(dst) {
				_, _ = t.rwc.Write(p) // Yggdrasil itself, not through the server
				continue
			}
			if !t.ipv6 {
				if r := unreachable(t.self, p); r != nil {
					_, _ = t.f.Write(r)
				}
				continue
			}
		}
		sizes[0] = n
		return 1, nil
	}
}

func (t *splitTun) Write(bufs [][]byte, offset int) (int, error) {
	for i, b := range bufs {
		if pings.catch(b[offset:]) {
			continue
		}
		if _, err := t.f.Write(b[offset:]); err != nil {
			return i, err
		}
	}
	return len(bufs), nil
}

func (t *splitTun) Close() error {
	t.once.Do(func() { close(t.events) })
	return t.f.Close()
}

// ---- wiring ------------------------------------------------------------------

func b64hex(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return "", errors.New("bad WireGuard key")
	}
	return hex.EncodeToString(b), nil
}

// parseTunnelConfig checks the config and returns the keys in the hex form of WireGuard's IPC and the server address.
func parseTunnelConfig(cfg TunnelConfig) (priv, pub string, server netip.Addr, err error) {
	if priv, err = b64hex(cfg.PrivateKey); err != nil {
		return
	}
	if pub, err = b64hex(cfg.ServerKey); err != nil {
		return
	}
	server, err = netip.ParseAddr(cfg.ServerYgg)
	if err != nil || !yggNet.Contains(server) {
		err = fmt.Errorf("bad server address %q", cfg.ServerYgg)
	}
	return
}

// wgIPC is the WireGuard configuration: one peer (the server) that gets all traffic.
func wgIPC(priv, pub string, server netip.Addr, port int) string {
	return strings.Join([]string{
		"private_key=" + priv,
		"replace_peers=true",
		"public_key=" + pub,
		"endpoint=" + netip.AddrPortFrom(server, uint16(port)).String(),
		"persistent_keepalive_interval=25",
		"replace_allowed_ips=true",
		"allowed_ip=0.0.0.0/0",
		"allowed_ip=::/0",
	}, "\n") + "\n"
}

// readFromYgg: Yggdrasil → WireGuard (datagrams from the server) or → TUN (everything else).
func readFromYgg(rwc *ipv6rwc.ReadWriteCloser, f *os.File, bind *yggBind, server netip.Addr, port uint16) {
	buf := make([]byte, int(rwc.MTU())+64)
	for {
		k, err := rwc.Read(buf)
		if err != nil {
			return
		}
		if from, data, ok := parseUDP(buf[:k], port); ok && from.Addr() == server {
			bind.deliver(from, data)
			continue
		}
		if pings.catch(buf[:k]) || tapUDP(buf[:k]) || yggStackTap(buf[:k]) {
			continue
		}
		if _, err := f.Write(buf[:k]); err != nil && errors.Is(err, os.ErrClosed) {
			return
		}
	}
}

// AttachTunnel is AttachTun for the full-tunnel mode.
func (n *Node) AttachTunnel(fd int, cfg TunnelConfig) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.rwc == nil {
		return errors.New("not running")
	}
	priv, pub, server, err := parseTunnelConfig(cfg)
	if err != nil {
		return err
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		return err
	}
	self := netip.MustParseAddr(net.IP(n.core.Address()).String())
	f := os.NewFile(uintptr(fd), "tun")
	t := &splitTun{f: f, rwc: n.rwc, self: self, ipv6: cfg.IPv6, probes: make(chan []byte, 8), events: make(chan tun.Event, 1)}
	t.ip4, _ = netip.ParseAddr(cfg.ClientIP4)
	t.events <- tun.EventUp
	bind := newYggBind(n.rwc, self, uint16(cfg.Port))
	var ls *lanes
	if cfg.Lanes > 1 {
		main := &lane{c: n.core, rwc: n.rwc, self: self}
		main.ready.Store(true)
		ls = &lanes{all: []*lane{main}, stop: make(chan struct{})}
		bind.withLanes(ls)
	}
	logger := &device.Logger{
		Verbosef: device.DiscardLogf,
		Errorf:   func(format string, a ...any) { logSink.add("WireGuard: " + fmt.Sprintf(format, a...)) },
	}
	dev := device.NewDevice(t, bind, logger)
	ipc := wgIPC(priv, pub, server, cfg.Port)
	if err := dev.IpcSet(ipc); err != nil {
		dev.Close()
		return err
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return err
	}
	n.tun, n.wg, n.split, n.lanes = f, dev, t, ls
	if ls != nil {
		startLanes(ls, cfg.Lanes, cfg.LaneURI, server, uint16(cfg.Port), bind, n.rwc.MTU())
		logSink.add("Lanes to the server: " + strconv.Itoa(cfg.Lanes))
	}
	go readFromYgg(n.rwc, f, bind, server, uint16(cfg.Port))
	logSink.add("Tunnel to " + server.String() + " port " + strconv.Itoa(cfg.Port))
	return nil
}

type tunnelStatus struct {
	HandshakeAgo float64 `json:"handshakeAgo"` // seconds, -1 — none yet
	Rx           uint64  `json:"rx"`
	Tx           uint64  `json:"tx"`
}

// tunnelStats reads the WireGuard peer counters; call with n.mu held.
func (n *Node) tunnelStats(now int64) *tunnelStatus {
	if n.wg == nil {
		return nil
	}
	s, err := n.wg.IpcGet()
	if err != nil {
		return nil
	}
	st := &tunnelStatus{HandshakeAgo: -1}
	for _, line := range strings.Split(s, "\n") {
		k, v, _ := strings.Cut(line, "=")
		x, _ := strconv.ParseUint(v, 10, 64)
		switch k {
		case "last_handshake_time_sec":
			if x > 0 {
				st.HandshakeAgo = float64(now - int64(x))
			}
		case "rx_bytes":
			st.Rx = x
		case "tx_bytes":
			st.Tx = x
		}
	}
	return st
}
