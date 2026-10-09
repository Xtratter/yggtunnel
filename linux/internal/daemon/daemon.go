// Package daemon is the state machine behind yggtunneld: it turns commands into core and network
// changes and undoes all of them when anything fails.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sync"
	"syscall"

	"github.com/Xtratter/yggtunnel/go/core"
	"github.com/Xtratter/yggtunnel/linux/internal/ipc"
	"github.com/Xtratter/yggtunnel/linux/internal/netconf"
	"github.com/Xtratter/yggtunnel/linux/internal/profile"
	"github.com/Xtratter/yggtunnel/linux/internal/store"
	"github.com/Xtratter/yggtunnel/linux/internal/version"
)

// Core is the seam over *core.Node.
type Core interface {
	Start(configJSON string, peers []string) (yggAddr string, err error)
	AttachTunnel(fd int, cfg core.TunnelConfig) error
	Stop()
	Status() string
	MTU() int
	Log() string
}

// Net is the seam over netconf and dns. Up registers every change in tx and returns the TUN device.
// Recover undoes the steps recorded by a process that died.
type Net interface {
	Up(tx *netconf.Tx, p netconf.Params, dns []netip.Addr) (*os.File, error)
	Recover(prev store.PrevState) error
}

// Tunnel parameters, the same as the Android app's.
const (
	ifName    = "yggtun0"
	tunnelMTU = 1280
	table     = 51871
	mark      = 0x5967
)

var dnsServers = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")}

// Daemon executes commands. Safe for concurrent use.
type Daemon struct {
	// CgroupPath is the absolute cgroup v2 directory of the daemon; its traffic bypasses the tunnel.
	CgroupPath string
	// GenConfig creates a fresh node config on first use.
	GenConfig func() (string, error)

	st   *store.Store
	c    Core
	n    Net
	emit func(ipc.Event)

	opMu    sync.Mutex // one state-changing operation at a time
	mu      sync.Mutex // guards state and lastErr
	state   State
	lastErr string
	tx      *netconf.Tx
}

// New creates a daemon in the Off state.
func New(st *store.Store, c Core, n Net, emit func(ipc.Event)) *Daemon {
	return &Daemon{st: st, c: c, n: n, emit: emit, state: Off, GenConfig: core.GenerateConfig}
}

// Handle implements ipc.Handler.
func (d *Daemon) Handle(_ context.Context, _ ipc.Peer, req ipc.Request) (any, error) {
	switch req.Cmd {
	case "up":
		return nil, d.up()
	case "down":
		return nil, d.down()
	case "panic":
		return nil, d.panicOff()
	case "status":
		return d.status(), nil
	case "import":
		var a struct {
			Link string `json:"link"`
		}
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return nil, errors.New("import needs {\"link\": ...}")
		}
		return nil, d.importLink(a.Link)
	case "log":
		return d.c.Log(), nil
	case "version":
		return version.String(), nil
	}
	return nil, fmt.Errorf("unknown command %q", req.Cmd)
}

func (d *Daemon) status() Status {
	d.mu.Lock()
	s := Status{State: d.state, Error: d.lastErr}
	d.mu.Unlock()
	if s.State == Connected || s.State == Reconnecting {
		s.Node = json.RawMessage(d.c.Status())
	}
	s.Profile = d.st.MaskedProfile()
	return s
}

func (d *Daemon) cur() State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

func (d *Daemon) importLink(link string) error {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	if d.cur() != Off {
		return errors.New("disconnect before importing a profile")
	}
	p, err := profile.Parse(link)
	if err != nil {
		return err
	}
	return d.st.SaveProfile(p)
}

// rec persists the recorded steps; an empty record removes the file.
func (d *Daemon) rec(p store.PrevState) error {
	if len(p.Steps) == 0 {
		return d.st.ClearPrev()
	}
	return d.st.SavePrev(p)
}

func (d *Daemon) up() error {
	if !d.opMu.TryLock() {
		return errors.New("already in progress: another operation is running")
	}
	defer d.opMu.Unlock()
	if s := d.cur(); s != Off {
		return fmt.Errorf("already %s", s)
	}
	prof, err := d.st.Profile()
	if err != nil {
		return err
	}
	d.setState(Starting, "")
	fail := func(err error) error {
		d.teardown()
		d.setState(Error, err.Error())
		d.setState(Off, "")
		return err
	}
	cfgJSON, err := d.st.NodeConfig(d.GenConfig)
	if err != nil {
		return fail(err)
	}
	var peers []string
	seen := map[string]bool{}
	for _, p := range append([]string{prof.DirectPeer, prof.WSSPeer}, prof.Peers...) {
		if p != "" && !seen[p] {
			seen[p] = true
			peers = append(peers, p)
		}
	}
	yggStr, err := d.c.Start(cfgJSON, peers)
	if err != nil {
		return fail(err)
	}
	p := netconf.Params{IfName: ifName, MTU: tunnelMTU, Table: table, Mark: mark, CgroupPath: d.CgroupPath}
	if p.YggAddr, err = netip.ParseAddr(yggStr); err != nil {
		return fail(fmt.Errorf("node address %q: %w", yggStr, err))
	}
	if p.ClientIP4, err = netip.ParseAddr(prof.ClientIP4); err != nil {
		return fail(fmt.Errorf("profile clientIp4 %q: %w", prof.ClientIP4, err))
	}
	if prof.IPv6 {
		if p.ClientIP6, err = netip.ParseAddr(prof.ClientIP6); err != nil {
			return fail(fmt.Errorf("profile clientIp6 %q: %w", prof.ClientIP6, err))
		}
	}
	d.tx = netconf.NewTx(d.rec)
	f, err := d.n.Up(d.tx, p, dnsServers)
	if err != nil {
		return fail(err)
	}
	// The core takes ownership of the descriptor it is given, so hand it a duplicate.
	fd, err := syscall.Dup(int(f.Fd()))
	f.Close()
	if err != nil {
		return fail(err)
	}
	if err := d.c.AttachTunnel(fd, prof.TunnelConfig()); err != nil {
		syscall.Close(fd)
		return fail(err)
	}
	d.setState(Connected, "")
	return nil
}

// teardown undoes the network changes and stops the core. Errors are logged by the caller's state
// text only when nothing else failed first; teardown itself never stops half way.
func (d *Daemon) teardown() error {
	var err error
	if d.tx != nil {
		err = d.tx.Rollback()
		d.tx = nil
	}
	d.c.Stop()
	return err
}

func (d *Daemon) down() error {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	if d.cur() == Off {
		return nil
	}
	err := d.teardown()
	d.setState(Off, "")
	return err
}

// panicOff removes everything the daemon may have left behind: the live connection and any steps
// recorded on disk by an earlier run.
func (d *Daemon) panicOff() error {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	var errs []error
	if d.cur() != Off {
		errs = append(errs, d.teardown())
		d.setState(Off, "")
	}
	errs = append(errs, d.recoverPrev())
	return errors.Join(errs...)
}

// Recover undoes the steps of a run that died; call it once at start.
func (d *Daemon) Recover() error {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	return d.recoverPrev()
}

func (d *Daemon) recoverPrev() error {
	prev, ok, err := d.st.Prev()
	if err != nil {
		_ = d.st.ClearPrev() // unreadable: nothing can be undone from it
		return err
	}
	if !ok {
		return nil
	}
	rerr := d.n.Recover(prev)
	if err := d.st.ClearPrev(); err != nil {
		rerr = errors.Join(rerr, err)
	}
	return rerr
}
