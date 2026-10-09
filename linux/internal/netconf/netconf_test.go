package netconf

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/Xtratter/yggtunnel/linux/internal/store"
	"github.com/vishvananda/netlink"
)

func params(t *testing.T) Params {
	return Params{
		IfName: "yggtun0", YggAddr: netip.MustParseAddr("200:db8::1"),
		ClientIP4: netip.MustParseAddr("192.0.2.10"), ClientIP6: netip.MustParseAddr("2001:db8::10"),
		MTU: 1280, Table: 51871, Mark: 0x5967, CgroupPath: ownCgroup(t),
	}
}

func TestCreateTunRemovesLeftover(t *testing.T) {
	if !inNetns(t) {
		return
	}
	if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "yggtun0"}}); err != nil {
		t.Fatal(err)
	}
	f, err := CreateTun("yggtun0")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	l, err := netlink.LinkByName("yggtun0")
	if err != nil {
		t.Fatal(err)
	}
	// netlink reports TUN devices as "tuntap"; what matters is that the leftover dummy is gone
	if l.Type() != "tuntap" {
		t.Fatalf("link type %q, want tuntap", l.Type())
	}
}

func TestConfigureThenRollbackRestoresRoutes(t *testing.T) {
	if !inNetns(t) {
		return
	}
	before := snapshot(t)
	f, err := CreateTun("yggtun0")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tx := NewTx(func(store.PrevState) error { return nil })
	if err := Configure(tx, params(t)); err != nil {
		t.Fatal(err)
	}
	during := snapshot(t)
	if during == before {
		t.Fatal("Configure changed nothing")
	}
	for _, want := range []string{"192.0.2.10", "200:db8::1", "2001:db8::10", "51871", "yggtunnel"} {
		if !contains(during, want) {
			t.Errorf("snapshot misses %q:\n%s", want, during)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if after := snapshot(t); after != before {
		t.Fatalf("rollback did not restore the state\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestRecoverFromRestoresRoutes(t *testing.T) {
	if !inNetns(t) {
		return
	}
	before := snapshot(t)
	f, err := CreateTun("yggtun0")
	if err != nil {
		t.Fatal(err)
	}
	var saved store.PrevState
	tx := NewTx(func(p store.PrevState) error { saved = p; return nil })
	if err := Configure(tx, params(t)); err != nil {
		t.Fatal(err)
	}
	// the daemon "dies": no rollback; only the persisted steps survive
	f.Close()
	if len(saved.Steps) == 0 {
		t.Fatal("nothing was persisted")
	}
	if err := RecoverFrom(saved); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t); after != before {
		t.Fatalf("recovery did not restore the state\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// recovering twice is harmless (steps may already be gone)
	if err := RecoverFrom(saved); err != nil {
		t.Fatalf("second recovery: %v", err)
	}
}

func TestConfigureOmitsIPv6WhenClientIP6Invalid(t *testing.T) {
	if !inNetns(t) {
		return
	}
	f, err := CreateTun("yggtun0")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p := params(t)
	p.ClientIP6 = netip.Addr{}
	tx := NewTx(func(store.PrevState) error { return nil })
	if err := Configure(tx, p); err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if s := snapshot(t); contains(s, "2001:db8::10") {
		t.Fatalf("IPv6 client address present:\n%s", s)
	}
}

func TestConfigureFailsWithoutCgroup(t *testing.T) {
	if !inNetns(t) {
		return
	}
	f, _ := CreateTun("yggtun0")
	defer f.Close()
	p := params(t)
	p.CgroupPath = ""
	tx := NewTx(func(store.PrevState) error { return nil })
	if err := Configure(tx, p); err == nil {
		t.Fatal("expected error without a cgroup path")
	}
	tx.Rollback()
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
