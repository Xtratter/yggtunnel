package daemon

import (
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Xtratter/yggtunnel/linux/internal/dns"
	"github.com/Xtratter/yggtunnel/linux/internal/netconf"
	"github.com/Xtratter/yggtunnel/linux/internal/nstest"
	"github.com/Xtratter/yggtunnel/linux/internal/store"
)

// nsNet is RealNet without DNS (systemd-resolved is not reachable from a throw-away namespace).
type nsNet struct{}

func (nsNet) Up(tx *netconf.Tx, p netconf.Params, _ []netip.Addr, _ dns.Options) (*os.File, error) {
	f, err := netconf.CreateTun(p.IfName)
	if err != nil {
		return nil, err
	}
	if err := netconf.Configure(tx, p); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (nsNet) Recover(prev store.PrevState) error { return netconf.RecoverFrom(prev) }

func (nsNet) Clear() error { return netconf.Clear() }

func (nsNet) SplitNames(addrs []netip.Addr) error { return netconf.UpdateNames(addrs) }

func (nsNet) SplitSubnets(p netconf.SplitParams) error { return netconf.UpdateSubnets(p) }

func (nsNet) UpdateDNSDomains([]string) error { return nil }

func (nsNet) KillSwitch(tx *netconf.Tx, on bool, p netconf.KSParams) error {
	if !on {
		return tx.Undo("killswitch")
	}
	return netconf.AddKillSwitch(tx, p)
}

func ipOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("ip %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func nsRig(t *testing.T) *rig {
	r := newRig(t)
	r.d.n = nsNet{}
	r.d.CgroupPath = nstest.OwnCgroup(t)
	r.importSample(t)
	return r
}

func TestIntegrationUpAndDownLeaveNoTrace(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	before := nstest.Snapshot(t)
	r := nsRig(t)
	if _, err := r.cmd("up"); err != nil {
		t.Fatal(err)
	}
	addr := ipOut(t, "addr", "show", "yggtun0")
	for _, want := range []string{"192.0.2.10", "mtu 1280", "200:db8::1", "2001:db8::10"} {
		if !strings.Contains(addr, want) {
			t.Errorf("addr misses %q:\n%s", want, addr)
		}
	}
	if rt := ipOut(t, "-4", "route", "show", "table", "51871"); !strings.Contains(rt, "default dev yggtun0") {
		t.Errorf("no default route in the tunnel table:\n%s", rt)
	}
	if ru := ipOut(t, "-4", "rule", "show"); !strings.Contains(ru, "not from all fwmark 0x5967 lookup 51871") ||
		!strings.Contains(ru, "suppress_prefixlength 0") {
		t.Errorf("rules:\n%s", ru)
	}
	if _, err := r.cmd("down"); err != nil {
		t.Fatal(err)
	}
	if after := nstest.Snapshot(t); after != before {
		t.Fatalf("down left changes\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestIntegrationRecoverAfterDaemonDied(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	before := nstest.Snapshot(t)
	r := nsRig(t)
	if _, err := r.cmd("up"); err != nil {
		t.Fatal(err)
	}
	// the process "dies": a new daemon starts on the same state directory
	r2 := newRig(t)
	r2.d.st = r.st
	r2.d.n = nsNet{}
	if err := r2.d.Recover(); err != nil {
		t.Fatal(err)
	}
	if after := nstest.Snapshot(t); after != before {
		t.Fatalf("recovery left changes\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, ok, _ := r.st.Prev(); ok {
		t.Fatal("prev record not cleared")
	}
}

func TestIntegrationLeftoverInterfaceDoesNotBlockUp(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	if out, err := exec.Command("ip", "link", "add", "yggtun0", "type", "dummy").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	r := nsRig(t)
	if _, err := r.cmd("up"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.cmd("down"); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationKillSwitchUpDown(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	before := nstest.Snapshot(t)
	r := nsRig(t)
	if err := r.set(t, `{"killSwitch":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.cmd("up"); err != nil {
		t.Fatal(err)
	}
	if !netconf.KillSwitchActive() {
		t.Fatal("the table is missing after up")
	}
	if _, err := r.cmd("down"); err != nil {
		t.Fatal(err)
	}
	if netconf.KillSwitchActive() {
		t.Fatal("the table survived down")
	}
	if after := nstest.Snapshot(t); after != before {
		t.Fatalf("down left changes\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestIntegrationKillSwitchCrashRecovery(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	r := nsRig(t)
	r.set(t, `{"killSwitch":true}`)
	if _, err := r.cmd("up"); err != nil {
		t.Fatal(err)
	}
	r2 := newRig(t) // a new process on the same state directory
	r2.d.st = r.st
	r2.d.n = nsNet{}
	if err := r2.d.Recover(); err != nil {
		t.Fatal(err)
	}
	if netconf.KillSwitchActive() {
		t.Fatal("the kill switch survived the crash: the user would be locked out")
	}
}

func TestIntegrationSplitOnlyThroughDaemon(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	before := nstest.Snapshot(t)
	r := nsRig(t)
	if err := r.set(t, `{"split":{"mode":"only","subnets":["203.0.113.0/24"]}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.cmd("up"); err != nil {
		t.Fatal(err)
	}
	rules := ipOut(t, "-4", "rule", "show")
	if !strings.Contains(rules, "fwmark 0x5968 lookup 51871") || strings.Contains(rules, "suppress_prefixlength") {
		t.Fatalf("rules for mode only:\n%s", rules)
	}
	out, _ := exec.Command("nft", "list", "table", "inet", "yggtunnel").CombinedOutput()
	if !strings.Contains(string(out), "203.0.113.0/24") && !strings.Contains(string(out), "203.0.113.0") {
		t.Fatalf("the listed subnet is not in the firewall:\n%s", out)
	}
	// a live list change while connected
	if err := r.set(t, `{"split":{"mode":"only","subnets":["198.51.100.0/24"]}}`); err != nil {
		t.Fatal(err)
	}
	out, _ = exec.Command("nft", "list", "table", "inet", "yggtunnel").CombinedOutput()
	if strings.Contains(string(out), "203.0.113.0") || !strings.Contains(string(out), "198.51.100.0") {
		t.Fatalf("the live change did not replace the rules:\n%s", out)
	}
	if _, err := r.cmd("down"); err != nil {
		t.Fatal(err)
	}
	if after := nstest.Snapshot(t); after != before {
		t.Fatalf("down left changes\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
