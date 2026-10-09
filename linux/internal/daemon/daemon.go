// Package daemon is the state machine behind yggtunneld: it turns commands into core and network
// changes and undoes all of them when anything fails.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"

	"github.com/Xtratter/yggtunnel/go/core"
	"github.com/Xtratter/yggtunnel/linux/internal/auth"
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
	// KillSwitch arms (on) or removes (off) the kill switch as a step of tx; arming again replaces it.
	KillSwitch(tx *netconf.Tx, on bool, p netconf.KSParams) error
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
	// Auth is asked before every state-changing command; nil allows all (development and tests only —
	// cmd/yggtunneld refuses to run without it outside dry-run).
	Auth auth.Authorizer
	// GenConfig creates a fresh node config on first use.
	GenConfig func() (string, error)
	// Lookup resolves peer host names before the tunnel takes over DNS.
	Lookup func(host string) ([]string, error)

	st   *store.Store
	c    Core
	n    Net
	emit func(ipc.Event)

	opMu    sync.Mutex // one state-changing operation at a time
	mu      sync.Mutex // guards state and lastErr
	state   State
	lastErr string
	tx      *netconf.Tx
	ksArmed bool // guarded by mu
}

// New creates a daemon in the Off state.
func New(st *store.Store, c Core, n Net, emit func(ipc.Event)) *Daemon {
	return &Daemon{st: st, c: c, n: n, emit: emit, state: Off, GenConfig: core.GenerateConfig, Lookup: net.LookupHost}
}

// Handle implements ipc.Handler.
func (d *Daemon) Handle(_ context.Context, peer ipc.Peer, req ipc.Request) (any, error) {
	switch req.Cmd {
	case "up", "down", "panic", "import", "set":
		if d.Auth != nil {
			if err := d.Auth.Check(peer, auth.ActionConnect); err != nil {
				return nil, err
			}
		}
	}
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
	case "set":
		return nil, d.set(req.Args)
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
	s.Settings = d.st.Settings()
	d.mu.Lock()
	s.KillSwitchActive = d.ksArmed
	d.mu.Unlock()
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
	// Names are resolved now: once the tunnel owns DNS, a peer that is down could not be looked up.
	yggStr, err := d.c.Start(cfgJSON, resolvePeers(peers, d.Lookup))
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
	if s := d.st.Settings(); s.KillSwitch { // armed last: the tunnel carries traffic before anything is dropped
		if err := d.armKillSwitch(s); err != nil {
			return fail(fmt.Errorf("kill switch: %w", err))
		}
	}
	d.setState(Connected, "")
	return nil
}

func (d *Daemon) armKillSwitch(s store.Settings) error {
	if err := d.n.KillSwitch(d.tx, true, netconf.KSParams{IfName: ifName, Mark: mark, AllowLAN: s.AllowLAN}); err != nil {
		return err
	}
	d.mu.Lock()
	d.ksArmed = true
	d.mu.Unlock()
	return nil
}

func (d *Daemon) disarmKillSwitch() error {
	err := d.n.KillSwitch(d.tx, false, netconf.KSParams{})
	d.mu.Lock()
	d.ksArmed = false
	d.mu.Unlock()
	return err
}

// set changes the settings. While connected the change is applied first and stored only if it
// worked; otherwise it is only stored (`up` reads it after the tunnel is attached). It waits for a
// running up/down, so a change made during `up` takes effect right after it.
func (d *Daemon) set(args json.RawMessage) error {
	var a struct {
		KillSwitch *bool `json:"killSwitch"`
		AllowLAN   *bool `json:"allowLan"`
	}
	if err := json.Unmarshal(args, &a); err != nil || (a.KillSwitch == nil && a.AllowLAN == nil) {
		return errors.New(`set needs {"killSwitch": bool} and/or {"allowLan": bool}`)
	}
	d.opMu.Lock()
	defer d.opMu.Unlock()
	s := d.st.Settings()
	if a.KillSwitch != nil {
		s.KillSwitch = *a.KillSwitch
	}
	if a.AllowLAN != nil {
		s.AllowLAN = *a.AllowLAN
	}
	if d.cur() == Connected {
		var err error
		if s.KillSwitch {
			err = d.armKillSwitch(s)
		} else {
			err = d.disarmKillSwitch()
		}
		if err != nil {
			return err
		}
	}
	return d.st.SaveSettings(s)
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
	d.mu.Lock()
	d.ksArmed = false // the table went with the transaction
	d.mu.Unlock()
	return err
}

// Shutdown undoes the connection when the daemon itself stops; it needs no authorisation.
func (d *Daemon) Shutdown() error { return d.down() }

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
	if err := d.n.Recover(prev); err != nil {
		return err // keep the record: every undo is idempotent, so panic or the next start can retry
	}
	return d.st.ClearPrev()
}
