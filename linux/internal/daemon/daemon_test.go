package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Xtratter/yggtunnel/go/core"
	"github.com/Xtratter/yggtunnel/linux/internal/ipc"
	"github.com/Xtratter/yggtunnel/linux/internal/netconf"
	"github.com/Xtratter/yggtunnel/linux/internal/profile"
	"github.com/Xtratter/yggtunnel/linux/internal/store"
)

type calls struct {
	mu sync.Mutex
	l  []string
}

func (c *calls) add(s string) { c.mu.Lock(); c.l = append(c.l, s); c.mu.Unlock() }
func (c *calls) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.l, ",")
}

type fakeCore struct {
	log      *calls
	startErr error
	attach   error
	gate     chan struct{} // when set, Start blocks until it is closed
	gotCfg   core.TunnelConfig
	gotFd    int
	gotCfgJS string
	gotPeers []string
}

func (f *fakeCore) Start(cfg string, peers []string) (string, error) {
	f.log.add("core.start")
	f.gotCfgJS = cfg
	f.gotPeers = peers
	if f.gate != nil {
		<-f.gate
	}
	if f.startErr != nil {
		return "", f.startErr
	}
	return "200:db8::1", nil
}
func (f *fakeCore) AttachTunnel(fd int, c core.TunnelConfig) error {
	f.log.add("core.attach")
	f.gotCfg, f.gotFd = c, fd
	return f.attach
}
func (f *fakeCore) Stop()          { f.log.add("core.stop") }
func (f *fakeCore) Status() string { return `{"peers":[]}` }
func (f *fakeCore) MTU() int       { return 1280 }
func (f *fakeCore) Log() string    { return "log line" }

type fakeNet struct {
	log       *calls
	failAfter int // steps that succeed before Up fails (-1: never fail)
	params    netconf.Params
	dns       []netip.Addr
	dir       string
	recovered store.PrevState
}

func (f *fakeNet) Up(tx *netconf.Tx, p netconf.Params, dns []netip.Addr) (*os.File, error) {
	f.log.add("net.up")
	f.params, f.dns = p, dns
	for i, name := range []string{"a", "b", "c"} {
		if f.failAfter >= 0 && i == f.failAfter {
			return nil, errors.New("step failed")
		}
		name := name
		if err := tx.Do(store.Step{Kind: "fake", Args: map[string]string{"n": name}},
			func() error { return nil },
			func() error { f.log.add("undo." + name); return nil }); err != nil {
			return nil, err
		}
	}
	return os.Create(filepath.Join(f.dir, "tun"))
}

func (f *fakeNet) Recover(p store.PrevState) error {
	f.log.add("net.recover")
	f.recovered = p
	return nil
}

type rig struct {
	d    *Daemon
	c    *fakeCore
	n    *fakeNet
	st   *store.Store
	log  *calls
	evs  []ipc.Event
	evMu sync.Mutex
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{log: &calls{}}
	st, err := store.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	r.st = st
	r.c = &fakeCore{log: r.log}
	r.n = &fakeNet{log: r.log, failAfter: -1, dir: t.TempDir()}
	r.d = New(st, r.c, r.n, func(e ipc.Event) { r.evMu.Lock(); r.evs = append(r.evs, e); r.evMu.Unlock() })
	r.d.CgroupPath = "/sys/fs/cgroup/test.slice/yggtunneld.service"
	r.d.GenConfig = func() (string, error) { return `{"PrivateKey":"x"}`, nil }
	return r
}

func (r *rig) importSample(t *testing.T) profile.Profile {
	t.Helper()
	p := profile.Profile{V: 1, Name: "example", PrivateKey: "cHJpdmF0ZQ==", ServerKey: "c2VydmVy", ServerYgg: "200:db8::2",
		Port: 51820, IPv6: true, ClientIP4: "192.0.2.10", ClientIP6: "2001:db8::10", Peers: []string{"tls://192.0.2.1:1234"}}
	args, _ := json.Marshal(map[string]string{"link": p.Link()})
	if _, err := r.d.Handle(context.Background(), ipc.Peer{}, ipc.Request{Cmd: "import", Args: args}); err != nil {
		t.Fatal(err)
	}
	return p
}

func (r *rig) cmd(cmd string) (any, error) {
	return r.d.Handle(context.Background(), ipc.Peer{}, ipc.Request{Cmd: cmd})
}

func (r *rig) state(t *testing.T) string {
	t.Helper()
	v, err := r.cmd("status")
	if err != nil {
		t.Fatal(err)
	}
	return string(v.(Status).State)
}

func (r *rig) states() []string {
	r.evMu.Lock()
	defer r.evMu.Unlock()
	var out []string
	for _, e := range r.evs {
		if e.Kind == "state" {
			var s struct{ State string }
			json.Unmarshal(e.Data, &s)
			out = append(out, s.State)
		}
	}
	return out
}

func TestUpHappyPathOrder(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	if _, err := r.cmd("up"); err != nil {
		t.Fatal(err)
	}
	if got := r.log.String(); got != "core.start,net.up,core.attach" {
		t.Fatalf("calls %s", got)
	}
	if r.state(t) != "connected" {
		t.Fatalf("state %s", r.state(t))
	}
	if s := strings.Join(r.states(), ","); s != "starting,connected" {
		t.Fatalf("events %s", s)
	}
	if r.n.params.YggAddr != netip.MustParseAddr("200:db8::1") || r.n.params.ClientIP4 != netip.MustParseAddr("192.0.2.10") ||
		r.n.params.IfName != "yggtun0" || r.n.params.MTU != 1280 || r.n.params.CgroupPath == "" {
		t.Fatalf("params %+v", r.n.params)
	}
	if len(r.n.dns) != 2 || r.n.dns[0] != netip.MustParseAddr("1.1.1.1") || r.n.dns[1] != netip.MustParseAddr("8.8.8.8") {
		t.Fatalf("dns %v", r.n.dns)
	}
	if r.c.gotCfg.ServerYgg != "200:db8::2" || r.c.gotFd <= 0 {
		t.Fatalf("attach got %+v fd %d", r.c.gotCfg, r.c.gotFd)
	}
}

func TestUpFailureRollsBack(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	r.n.failAfter = 2
	if _, err := r.cmd("up"); err == nil {
		t.Fatal("expected error")
	}
	if got := r.log.String(); got != "core.start,net.up,undo.b,undo.a,core.stop" {
		t.Fatalf("calls %s", got)
	}
	if r.state(t) != "off" {
		t.Fatalf("state %s", r.state(t))
	}
	if _, ok, _ := r.st.Prev(); ok {
		t.Fatal("prev record not cleared")
	}
	v, _ := r.cmd("status")
	if !strings.Contains(v.(Status).Error, "step failed") {
		t.Fatalf("status error %q", v.(Status).Error)
	}
}

func TestUpAttachFailureRollsBack(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	r.c.attach = errors.New("attach failed")
	if _, err := r.cmd("up"); err == nil {
		t.Fatal("expected error")
	}
	if got := r.log.String(); got != "core.start,net.up,core.attach,undo.c,undo.b,undo.a,core.stop" {
		t.Fatalf("calls %s", got)
	}
}

func TestCoreStartFailureStopsNothingElse(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	r.c.startErr = errors.New("no peers")
	if _, err := r.cmd("up"); err == nil {
		t.Fatal("expected error")
	}
	if got := r.log.String(); got != "core.start,core.stop" {
		t.Fatalf("calls %s", got)
	}
}

func TestSecondUpWhileStartingRefused(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	r.c.gate = make(chan struct{})
	done := make(chan error, 1)
	go func() { _, err := r.cmd("up"); done <- err }()
	for i := 0; r.state(t) != "starting"; i++ {
		if i > 1000 {
			t.Fatal("never reached starting")
		}
	}
	if _, err := r.cmd("up"); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second up: %v", err)
	}
	close(r.c.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r.state(t) != "connected" {
		t.Fatalf("state %s", r.state(t))
	}
	if strings.Count(r.log.String(), "core.start") != 1 {
		t.Fatalf("core started twice: %s", r.log)
	}
}

func TestUpWhenConnectedRefused(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	r.cmd("up")
	if _, err := r.cmd("up"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDownRollsBackConnection(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	r.cmd("up")
	if _, err := r.cmd("down"); err != nil {
		t.Fatal(err)
	}
	if got := r.log.String(); got != "core.start,net.up,core.attach,undo.c,undo.b,undo.a,core.stop" {
		t.Fatalf("calls %s", got)
	}
	if r.state(t) != "off" {
		t.Fatalf("state %s", r.state(t))
	}
	if s := strings.Join(r.states(), ","); s != "starting,connected,off" {
		t.Fatalf("events %s", s)
	}
}

func TestDownWhenOffIsNoop(t *testing.T) {
	r := newRig(t)
	if _, err := r.cmd("down"); err != nil {
		t.Fatal(err)
	}
	if r.log.String() != "" {
		t.Fatalf("calls %s", r.log)
	}
}

func TestPanicWhenOffIsNoop(t *testing.T) {
	r := newRig(t)
	if _, err := r.cmd("panic"); err != nil {
		t.Fatal(err)
	}
	if r.log.String() != "" {
		t.Fatalf("calls %s", r.log)
	}
}

func TestPanicRollsBackFromPrevFile(t *testing.T) {
	r := newRig(t)
	r.st.SavePrev(store.PrevState{Steps: []store.Step{{Kind: "addr"}}})
	if _, err := r.cmd("panic"); err != nil {
		t.Fatal(err)
	}
	if r.log.String() != "net.recover" || len(r.n.recovered.Steps) != 1 {
		t.Fatalf("calls %s recovered %+v", r.log, r.n.recovered)
	}
	if _, ok, _ := r.st.Prev(); ok {
		t.Fatal("prev not cleared")
	}
}

func TestRecoverRollsBackLeftoverPrev(t *testing.T) {
	r := newRig(t)
	r.st.SavePrev(store.PrevState{Steps: []store.Step{{Kind: "route"}, {Kind: "rule"}}})
	if err := r.d.Recover(); err != nil {
		t.Fatal(err)
	}
	if len(r.n.recovered.Steps) != 2 {
		t.Fatalf("recovered %+v", r.n.recovered)
	}
	if _, ok, _ := r.st.Prev(); ok {
		t.Fatal("prev not cleared")
	}
}

func TestRecoverWithNothingIsNoop(t *testing.T) {
	r := newRig(t)
	if err := r.d.Recover(); err != nil || r.log.String() != "" {
		t.Fatalf("err=%v calls=%s", err, r.log)
	}
}

func TestUpWithoutProfileErrors(t *testing.T) {
	r := newRig(t)
	if _, err := r.cmd("up"); err == nil || !strings.Contains(err.Error(), "profile") {
		t.Fatalf("err=%v", err)
	}
	if r.log.String() != "" {
		t.Fatalf("calls %s", r.log)
	}
}

func TestImportStoresProfileAndStatusIsMasked(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	v, err := r.cmd("status")
	if err != nil {
		t.Fatal(err)
	}
	s := v.(Status)
	if s.Profile.ServerYgg != "200:db8::2" || strings.Contains(s.Profile.PrivateKey, "cHJpdmF0ZQ") || s.Profile.PrivateKey != "…ZQ==" {
		t.Fatalf("profile %+v", s.Profile)
	}
}

func TestImportRejectsBadLinkAndKeepsOldProfile(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	args, _ := json.Marshal(map[string]string{"link": "yggtunnel://import#!!!"})
	if _, err := r.d.Handle(context.Background(), ipc.Peer{}, ipc.Request{Cmd: "import", Args: args}); err == nil {
		t.Fatal("expected error")
	}
	if v, _ := r.cmd("status"); v.(Status).Profile.ServerYgg != "200:db8::2" {
		t.Fatal("old profile lost")
	}
}

func TestImportWhileConnectedRefused(t *testing.T) {
	r := newRig(t)
	p := r.importSample(t)
	r.cmd("up")
	args, _ := json.Marshal(map[string]string{"link": p.Link()})
	if _, err := r.d.Handle(context.Background(), ipc.Peer{}, ipc.Request{Cmd: "import", Args: args}); err == nil {
		t.Fatal("import must be refused while connected")
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, err := newRig(t).cmd("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionAndLog(t *testing.T) {
	r := newRig(t)
	if v, err := r.cmd("version"); err != nil || v == "" {
		t.Fatalf("%v %v", v, err)
	}
	if v, err := r.cmd("log"); err != nil || v != "log line" {
		t.Fatalf("%v %v", v, err)
	}
}

func TestNodeConfigPersistsAcrossUps(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	n := 0
	r.d.GenConfig = func() (string, error) { n++; return `{"PrivateKey":"x"}`, nil }
	r.cmd("up")
	r.cmd("down")
	r.cmd("up")
	if n != 1 {
		t.Fatalf("node config generated %d times", n)
	}
}

type denyAll struct{ asked []string }

func (d *denyAll) Check(_ ipc.Peer, action string) error {
	d.asked = append(d.asked, action)
	return errors.New("not authorized by polkit")
}

func TestHandleRefusesWhenAuthorizerDenies(t *testing.T) {
	r := newRig(t)
	r.importSample(t) // imported before the authorizer is installed
	r.log.l = nil
	a := &denyAll{}
	r.d.Auth = a
	for _, cmd := range []string{"up", "down", "panic", "import"} {
		_, err := r.d.Handle(context.Background(), ipc.Peer{PID: 1}, ipc.Request{Cmd: cmd, Args: json.RawMessage(`{"link":"x"}`)})
		if err == nil || !strings.Contains(err.Error(), "not authorized") {
			t.Errorf("%s: err=%v", cmd, err)
		}
	}
	if r.log.String() != "" || r.state(t) != "off" {
		t.Fatalf("a refused command changed something: calls=%s state=%s", r.log, r.state(t))
	}
	if len(a.asked) != 4 || a.asked[0] != "io.github.xtratter.yggtunnel.connect" {
		t.Fatalf("asked %v", a.asked)
	}
}

func TestReadOnlyCommandsSkipAuthorizer(t *testing.T) {
	r := newRig(t)
	a := &denyAll{}
	r.d.Auth = a
	for _, cmd := range []string{"status", "log", "version"} {
		if _, err := r.d.Handle(context.Background(), ipc.Peer{PID: 1}, ipc.Request{Cmd: cmd}); err != nil {
			t.Errorf("%s: %v", cmd, err)
		}
	}
	if len(a.asked) != 0 {
		t.Fatalf("asked %v", a.asked)
	}
}

// I1: stopping the daemon must undo the connection even though the caller (the daemon itself)
// has no polkit identity.
func TestShutdownTearsDownWithoutAuthorization(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	r.cmd("up")
	r.d.Auth = &denyAll{}
	if err := r.d.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(r.log.String(), "undo.c,undo.b,undo.a,core.stop") {
		t.Fatalf("calls %s", r.log)
	}
	if r.d.cur() != Off {
		t.Fatalf("state %s", r.d.cur())
	}
}

func TestShutdownWhenOffIsNoop(t *testing.T) {
	r := newRig(t)
	if err := r.d.Shutdown(); err != nil || r.log.String() != "" {
		t.Fatalf("err=%v calls=%s", err, r.log)
	}
}

// I3: a failed recovery must keep the record, so that `panic` or the next start can retry.
type failingRecover struct {
	fakeNet
	err error
}

func (f *failingRecover) Recover(p store.PrevState) error { f.log.add("net.recover"); return f.err }

func TestFailedRecoveryKeepsRecord(t *testing.T) {
	r := newRig(t)
	fr := &failingRecover{fakeNet: *r.n, err: errors.New("netlink busy")}
	r.d.n = fr
	r.st.SavePrev(store.PrevState{Steps: []store.Step{{Kind: "rule"}}})
	if err := r.d.Recover(); err == nil {
		t.Fatal("expected the recovery error")
	}
	if _, ok, _ := r.st.Prev(); !ok {
		t.Fatal("the undo record was deleted although recovery failed")
	}
	fr.err = nil
	if err := r.d.Recover(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.st.Prev(); ok {
		t.Fatal("record not cleared after a successful retry")
	}
}

// I2: peer host names are resolved before the tunnel takes over DNS.
func TestResolvePeers(t *testing.T) {
	lookup := func(host string) ([]string, error) {
		switch host {
		case "peer.example.net":
			return []string{"2001:db8::7", "192.0.2.7"}, nil
		}
		return nil, errors.New("no such host")
	}
	got := resolvePeers([]string{
		"tls://peer.example.net:1234",
		"wss://peer.example.net:443/path?priority=1",
		"tls://192.0.2.9:1234",
		"tls://[2001:db8::9]:1234",
		"tls://unknown.example.net:1234",
		"tcp://peer.example.net:80?sni=custom.example.net",
		"not a uri",
	}, lookup)
	want := []string{
		"tls://192.0.2.7:1234?sni=peer.example.net",
		"wss://192.0.2.7:443/path?priority=1&sni=peer.example.net",
		"tls://192.0.2.9:1234",
		"tls://[2001:db8::9]:1234",
		"tls://unknown.example.net:1234",
		"tcp://192.0.2.7:80?sni=custom.example.net",
		"not a uri",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("peer %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestUpResolvesPeersBeforeNet(t *testing.T) {
	r := newRig(t)
	r.importSample(t)
	var order []string
	r.d.Lookup = func(h string) ([]string, error) {
		order = append(order, "lookup:"+h)
		return []string{"192.0.2.50"}, nil
	}
	p := profile.Profile{V: 1, PrivateKey: "cHJpdmF0ZQ==", ServerKey: "c2VydmVy", ServerYgg: "200:db8::2", Port: 51820,
		ClientIP4: "192.0.2.10", Peers: []string{"tls://host.example.net:1234"}}
	args, _ := json.Marshal(map[string]string{"link": p.Link()})
	r.d.Handle(context.Background(), ipc.Peer{}, ipc.Request{Cmd: "import", Args: args})
	r.cmd("up")
	if len(order) != 1 || order[0] != "lookup:host.example.net" || !strings.HasPrefix(r.log.String(), "core.start,net.up") {
		t.Fatalf("order %v calls %s", order, r.log)
	}
	if r.c.gotPeers[0] != "tls://192.0.2.50:1234?sni=host.example.net" {
		t.Fatalf("peers passed to the core: %v", r.c.gotPeers)
	}
}
