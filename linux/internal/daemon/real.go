package daemon

import (
	"errors"
	"log"
	"net"
	"net/netip"
	"os"
	"sync"

	"github.com/Xtratter/yggtunnel/go/core"
	"github.com/Xtratter/yggtunnel/linux/internal/dns"
	"github.com/Xtratter/yggtunnel/linux/internal/netconf"
	"github.com/Xtratter/yggtunnel/linux/internal/store"
)

// RealCore drives the process-wide core node.
type RealCore struct{}

func (RealCore) Start(cfg string, peers []string) (string, error) {
	return core.Default().Start(cfg, peers)
}
func (RealCore) AttachTunnel(fd int, c core.TunnelConfig) error {
	return core.Default().AttachTunnel(fd, c)
}
func (RealCore) Stop()          { core.Default().Stop() }
func (RealCore) Status() string { return core.Default().Status() }
func (RealCore) MTU() int       { return core.Default().MTU() }
func (RealCore) Log() string    { return core.LogString() }

// RealNet changes the host network: TUN, addresses, routes, rules, traffic mark, DNS.
type RealNet struct {
	mu      sync.Mutex
	conn    dns.Conn
	ifIndex int
}

func (n *RealNet) Up(tx *netconf.Tx, p netconf.Params, servers []netip.Addr, o dns.Options) (*os.File, error) {
	f, err := netconf.CreateTun(p.IfName)
	if err != nil {
		return nil, err
	}
	if err := netconf.Configure(tx, p); err != nil {
		f.Close()
		return nil, err
	}
	conn, err := dns.SystemConn()
	if err != nil {
		f.Close()
		return nil, err
	}
	ifc, err := net.InterfaceByName(p.IfName)
	if err != nil {
		f.Close()
		return nil, err
	}
	args := map[string]string{"if": p.IfName}
	if err := tx.Do(store.Step{Kind: "dns", Args: args},
		func() error { return dns.SetWith(conn, ifc.Index, servers, o) },
		func() error { return dns.Revert(conn, ifc.Index) }); err != nil {
		f.Close()
		return nil, err
	}
	n.mu.Lock()
	n.conn, n.ifIndex = conn, ifc.Index
	n.mu.Unlock()
	return f, nil
}

func (*RealNet) SplitNames(addrs []netip.Addr) error { return netconf.UpdateNames(addrs) }

func (*RealNet) SplitSubnets(p netconf.SplitParams) error { return netconf.UpdateSubnets(p) }

// UpdateDNSDomains changes which names the tunnel's DNS servers are asked for (mode "only").
func (n *RealNet) UpdateDNSDomains(domains []string) error {
	n.mu.Lock()
	conn, idx := n.conn, n.ifIndex
	n.mu.Unlock()
	if conn == nil {
		return errors.New("the tunnel's DNS is not set")
	}
	return dns.SetWith(conn, idx, dnsServers, dns.Options{Domains: domains})
}

// Clear removes the daemon's nftables tables by name.
func (*RealNet) Clear() error { return netconf.Clear() }

// KillSwitch arms or removes the kill switch table as a step of tx.
func (*RealNet) KillSwitch(tx *netconf.Tx, on bool, p netconf.KSParams) error {
	if !on {
		return tx.Undo("killswitch")
	}
	return netconf.AddKillSwitch(tx, p)
}

// Recover undoes recorded steps: DNS first (it was applied last), then the network steps.
func (*RealNet) Recover(prev store.PrevState) error {
	var rest store.PrevState
	var errs []error
	for _, s := range prev.Steps {
		if s.Kind != "dns" {
			rest.Steps = append(rest.Steps, s)
			continue
		}
		ifc, err := net.InterfaceByName(s.Args["if"])
		if err != nil {
			continue // the link is gone, and resolved dropped its settings with it
		}
		conn, err := dns.SystemConn()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, dns.Revert(conn, ifc.Index))
	}
	errs = append(errs, netconf.RecoverFrom(rest))
	return errors.Join(errs...)
}

// DryNet only logs what RealNet would do; nothing on the host changes. The TUN is a pipe.
type DryNet struct{}

func (DryNet) Up(tx *netconf.Tx, p netconf.Params, servers []netip.Addr, _ dns.Options) (*os.File, error) {
	for _, step := range []string{"link", "addresses", "routes+rules", "mark", "dns"} {
		step := step
		if err := tx.Do(store.Step{Kind: "dry", Args: map[string]string{"step": step}},
			func() error {
				log.Printf("dry-run: would apply %s for %s (table %d, mark %#x, dns %v)", step, p.IfName, p.Table, p.Mark, servers)
				return nil
			},
			func() error { log.Printf("dry-run: would undo %s", step); return nil }); err != nil {
			return nil, err
		}
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	_ = w // kept open for the process lifetime, so reads on r block instead of failing
	return r, nil
}

func (DryNet) KillSwitch(tx *netconf.Tx, on bool, p netconf.KSParams) error {
	if !on {
		return tx.Undo("dry-killswitch")
	}
	return tx.Do(store.Step{Kind: "dry-killswitch"},
		func() error { log.Printf("dry-run: would arm the kill switch (lan=%v)", p.AllowLAN); return nil },
		func() error { log.Printf("dry-run: would remove the kill switch"); return nil })
}

func (DryNet) SplitNames(addrs []netip.Addr) error {
	log.Printf("dry-run: would put %d addresses into the split sets", len(addrs))
	return nil
}

func (DryNet) SplitSubnets(p netconf.SplitParams) error {
	log.Printf("dry-run: would set %d split subnets (mode %s)", len(p.Subnets), p.Mode)
	return nil
}

func (DryNet) UpdateDNSDomains(domains []string) error {
	log.Printf("dry-run: would route %d names to the tunnel's DNS", len(domains))
	return nil
}

func (DryNet) Clear() error {
	log.Print("dry-run: would remove the daemon's nftables tables by name")
	return nil
}

func (DryNet) Recover(prev store.PrevState) error {
	log.Printf("dry-run: would recover %d recorded steps", len(prev.Steps))
	return nil
}
