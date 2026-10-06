// Package main is the native core of YggTunnel: a Yggdrasil node that the
// Android app drives through JNI (see jni.go). Built as libygg.so.
package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/gologme/log"
	"golang.zx2c4.com/wireguard/device"

	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/ipv6rwc"
	"github.com/yggdrasil-network/yggdrasil-go/src/version"
)

// Node is one running Yggdrasil instance plus the TUN pump attached to it.
type Node struct {
	mu      sync.Mutex
	core    *core.Core
	rwc     *ipv6rwc.ReadWriteCloser
	tun     *os.File
	wg      *device.Device // full-tunnel mode only
	split   *splitTun      // full-tunnel mode only
	lanes   *lanes         // extra links to the server for the tunnel (lanes.go), nil — none
	picker  *peerPicker
	started time.Time
}

var node Node

// GenerateConfig returns a fresh node config (new private key) as JSON.
func GenerateConfig() (string, error) {
	cfg := config.GenerateConfig()
	cfg.IfName = "none"
	cfg.AdminListen = "none"
	cfg.MulticastInterfaces = nil
	b, err := json.Marshal(cfg)
	return string(b), err
}

// Start launches the node. peers overrides the config's Peers when non-empty.
// Returns the node's Yggdrasil address; the TUN is attached later via AttachTun,
// because Android needs the address to build the interface.
func (n *Node) Start(configJSON string, peers []string) (string, error) {
	return n.StartWith(configJSON, peers, 0, nil)
}

// StartWith is Start with peer auto-pick: keep the [keep] fastest peers (0 — all) plus the [pinned] ones.
func (n *Node) StartWith(configJSON string, peers []string, keep int, pinned []string, serverOnly ...bool) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.core != nil {
		return "", errors.New("already running")
	}
	startLowat()
	cfg := config.GenerateConfig()
	if err := cfg.UnmarshalHJSON([]byte(configJSON)); err != nil {
		return "", err
	}
	if len(peers) > 0 {
		cfg.Peers = peers
	}
	logger := log.New(logSink, "", 0)
	for _, l := range []string{"error", "warn", "info"} {
		logger.EnableLevel(l)
	}
	ygg := net.IPNet{IP: net.ParseIP("200::"), Mask: net.CIDRMask(7, 128)}
	opts := []core.SetupOption{
		// Never try to peer over Yggdrasil itself.
		core.PeerFilter(func(ip net.IP) bool { return !ygg.Contains(ip) }),
	}
	for _, p := range cfg.Peers {
		opts = append(opts, core.Peer{URI: p})
	}
	c, err := core.New(cfg.Certificate, logger, opts...)
	if err != nil {
		return "", err
	}
	n.core = c
	n.rwc = ipv6rwc.NewReadWriteCloser(c)
	mtu := cfg.IfMTU
	if n.rwc.MaxMTU() < mtu {
		mtu = n.rwc.MaxMTU()
	}
	n.rwc.SetMTU(mtu)
	n.started = time.Now()
	n.startPicker(cfg.Peers, keep, pinned, len(serverOnly) > 0 && serverOnly[0])
	logger.Infof("Yggdrasil %s, address %s", version.BuildVersion(), c.Address())
	return net.IP(c.Address()).String(), nil
}

// MTU of the Yggdrasil interface, valid after Start.
func (n *Node) MTU() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.rwc == nil {
		return 0
	}
	return int(n.rwc.MTU())
}

// AttachTun takes ownership of the TUN file descriptor from VpnService and
// pumps IPv6 packets between it and Yggdrasil.
func (n *Node) AttachTun(fd int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.rwc == nil {
		return errors.New("not running")
	}
	// Non-blocking makes the fd go through Go's poller, so Close interrupts reads.
	if err := syscall.SetNonblock(fd, true); err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "tun")
	n.tun = f
	rwc := n.rwc
	mtu := int(rwc.MTU())
	go func() { // TUN -> Yggdrasil
		buf := make([]byte, mtu+64)
		for {
			k, err := f.Read(buf)
			if err != nil {
				return
			}
			if k > 0 && buf[0]>>4 == 6 {
				_, _ = rwc.Write(buf[:k])
			}
		}
	}()
	go func() { // Yggdrasil -> TUN
		buf := make([]byte, mtu+64)
		for {
			k, err := rwc.Read(buf)
			if err != nil {
				return
			}
			if pings.catch(buf[:k]) || tapUDP(buf[:k]) || yggStackTap(buf[:k]) {
				continue
			}
			if _, err := f.Write(buf[:k]); err != nil && errors.Is(err, os.ErrClosed) {
				return
			}
		}
	}()
	return nil
}

// Stop shuts the node down and closes the TUN.
func (n *Node) Stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	closeYggStack()
	if n.wg != nil {
		n.wg.Close() // closes the TUN too
		n.wg, n.tun, n.split = nil, nil, nil
	}
	if n.lanes != nil {
		n.lanes.close()
		n.lanes = nil
	}
	if n.tun != nil {
		_ = n.tun.Close()
		n.tun = nil
	}
	if n.rwc != nil {
		_ = n.rwc.Close()
		n.rwc = nil
	}
	n.stopPicker()
	if n.core != nil {
		n.core.Stop()
		n.core = nil
		logSink.add("Stopped")
	}
}

// RetryPeers dials all peers right away (e.g. after the network changed).
func (n *Node) RetryPeers() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.core != nil {
		n.core.RetryPeersNow()
	}
}

type peerStatus struct {
	URI       string  `json:"uri"`
	Up        bool    `json:"up"`
	Inbound   bool    `json:"inbound"`
	Address   string  `json:"address,omitempty"`
	LatencyMs float64 `json:"latencyMs"`
	RxBytes   uint64  `json:"rx"`
	TxBytes   uint64  `json:"tx"`
	UptimeS   float64 `json:"uptime"`
	Error     string  `json:"error,omitempty"`
}

type status struct {
	Running   bool          `json:"running"`
	Version   string        `json:"version"`
	Address   string        `json:"address,omitempty"`
	Subnet    string        `json:"subnet,omitempty"`
	PublicKey string        `json:"publicKey,omitempty"`
	UptimeS   float64       `json:"uptime"`
	Peers     []peerStatus  `json:"peers"`
	Routing   uint64        `json:"routing"`
	Tunnel    *tunnelStatus `json:"tunnel,omitempty"`
	Reserve   []string      `json:"reserve"`
}

// Status returns the node state as JSON for the UI.
func (n *Node) Status() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := status{Version: version.BuildVersion(), Peers: []peerStatus{}, Reserve: n.reserved()}
	if c := n.core; c != nil {
		s.Running = true
		s.Address = net.IP(c.Address()).String()
		sn := c.Subnet()
		s.Subnet = sn.String()
		self := c.GetSelf()
		s.PublicKey = hex.EncodeToString(self.Key)
		s.Routing = self.RoutingEntries
		s.UptimeS = time.Since(n.started).Seconds()
		s.Tunnel = n.tunnelStats(time.Now().Unix())
		for _, p := range c.GetPeers() {
			ps := peerStatus{
				URI: p.URI, Up: p.Up, Inbound: p.Inbound,
				LatencyMs: float64(p.Latency.Microseconds()) / 1000,
				RxBytes:   p.RXBytes, TxBytes: p.TXBytes, UptimeS: p.Uptime.Seconds(),
			}
			if a := address.AddrForKey(p.Key); a != nil && p.Up {
				ps.Address = net.IP(a[:]).String()
			}
			if p.LastError != nil {
				ps.Error = p.LastError.Error()
			}
			s.Peers = append(s.Peers, ps)
		}
	}
	b, _ := json.Marshal(s)
	return string(b)
}
