package netconf

import (
	"net"
	"net/netip"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/Xtratter/yggtunnel/linux/internal/nstest"
	"github.com/Xtratter/yggtunnel/linux/internal/store"
	"github.com/vishvananda/netlink"
)

func params(t *testing.T) Params {
	return Params{
		IfName: "yggtun0", YggAddr: netip.MustParseAddr("200:db8::1"),
		ClientIP4: netip.MustParseAddr("192.0.2.10"), ClientIP6: netip.MustParseAddr("2001:db8::10"),
		MTU: 1280, Table: 51871, Mark: 0x5967, CgroupPath: nstest.OwnCgroup(t),
	}
}

func TestCreateTunRemovesLeftover(t *testing.T) {
	if !nstest.InNetns(t) {
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
	if !nstest.InNetns(t) {
		return
	}
	before := nstest.Snapshot(t)
	f, err := CreateTun("yggtun0")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tx := NewTx(func(store.PrevState) error { return nil })
	if err := Configure(tx, params(t)); err != nil {
		t.Fatal(err)
	}
	during := nstest.Snapshot(t)
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
	if after := nstest.Snapshot(t); after != before {
		t.Fatalf("rollback did not restore the state\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestRecoverFromRestoresRoutes(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	before := nstest.Snapshot(t)
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
	if after := nstest.Snapshot(t); after != before {
		t.Fatalf("recovery did not restore the state\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// recovering twice is harmless (steps may already be gone)
	if err := RecoverFrom(saved); err != nil {
		t.Fatalf("second recovery: %v", err)
	}
}

func TestConfigureOmitsIPv6WhenClientIP6Invalid(t *testing.T) {
	if !nstest.InNetns(t) {
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
	if s := nstest.Snapshot(t); contains(s, "2001:db8::10") {
		t.Fatalf("IPv6 client address present:\n%s", s)
	}
}

func TestConfigureFailsWithoutCgroup(t *testing.T) {
	if !nstest.InNetns(t) {
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

// A socket opened after Configure (a peer dialled later, a reconnect) must leave through the
// physical link with that link's source address. The kernel picks the source at connect(),
// before the packet is marked, so without masquerading the tunnel's address would escape.
func TestMarkedTrafficLeavesWithPhysicalSourceAddress(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	for _, c := range [][]string{
		{"link", "add", "lan", "type", "dummy"},
		{"addr", "add", "198.51.100.2/24", "dev", "lan"},
		{"link", "set", "lan", "up"},
		{"route", "add", "default", "via", "198.51.100.1", "dev", "lan"},
	} {
		if out, err := exec.Command("ip", c...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %v\n%s", c, err, out)
		}
	}
	f, err := CreateTun("yggtun0")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tx := NewTx(func(store.PrevState) error { return nil })
	if err := Configure(tx, params(t)); err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// counters after source NAT (priority 200 > srcnat 100) see what really leaves
	rules := `table inet yggtest {
	chain post { type filter hook postrouting priority 200; policy accept;
		oifname "lan" ip saddr 192.0.2.10 counter comment "tunnel"
		oifname "lan" ip saddr 198.51.100.2 counter comment "physical"
	}
}`
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nft: %v\n%s", err, out)
	}
	defer exec.Command("nft", "delete", "table", "inet", "yggtest").Run()
	c, err := net.Dial("udp4", "203.0.113.9:9")
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("x"))
	c.Close()
	out, _ := exec.Command("nft", "list", "table", "inet", "yggtest").CombinedOutput()
	got := string(out)
	if !regexp.MustCompile(`saddr 198\.51\.100\.2 counter packets 1 `).MatchString(got) ||
		!regexp.MustCompile(`saddr 192\.0\.2\.10 counter packets 0 `).MatchString(got) {
		t.Fatalf("the packet left with the wrong source address:\n%s", got)
	}
}

// A rule or route left by an earlier run must not make every later Configure fail with EEXIST.
func TestConfigureToleratesLeftoverRulesAndRoutes(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	for _, c := range [][]string{
		{"rule", "add", "priority", "32763", "lookup", "main", "suppress_prefixlength", "0"},
		{"rule", "add", "priority", "32764", "not", "fwmark", "0x5967", "lookup", "51871"},
	} {
		if out, err := exec.Command("ip", c...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %v\n%s", c, err, out)
		}
	}
	f, err := CreateTun("yggtun0")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tx := NewTx(func(store.PrevState) error { return nil })
	if err := Configure(tx, params(t)); err != nil {
		t.Fatalf("leftover rules blocked Configure: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
