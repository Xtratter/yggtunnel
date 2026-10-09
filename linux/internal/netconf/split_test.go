package netconf

import (
	"errors"
	"net"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Xtratter/yggtunnel/linux/internal/nstest"
	"github.com/Xtratter/yggtunnel/linux/internal/store"
	"github.com/google/nftables"
)

// splitEnv builds the usual namespace (physical link `lan` with an IPv4 and an IPv6 address, default
// via 192.168.77.1), brings the tunnel up in the given split mode, and installs a counter table that
// sees where packets really leave (after source NAT).
func splitEnv(t *testing.T, mode string, subnets []string, cgroup string) *Tx {
	t.Helper()
	return splitEnvOpt(t, mode, subnets, cgroup, true)
}

// splitEnvOpt is splitEnv with the choice whether the tunnel's DNS servers are forced into the tunnel.
func splitEnvOpt(t *testing.T, mode string, subnets []string, cgroup string, forceDNS bool) *Tx {
	t.Helper()
	for _, c := range [][]string{
		{"link", "add", "lan", "type", "dummy"},
		{"addr", "add", "192.168.77.2/24", "dev", "lan"},
		{"addr", "add", "2001:db8:77::2/64", "dev", "lan", "nodad"},
		{"link", "set", "lan", "up"},
		{"route", "add", "default", "via", "192.168.77.1", "dev", "lan"},
	} {
		if out, err := exec.Command("ip", c...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %v\n%s", c, err, out)
		}
	}
	f, err := CreateTun("yggtun0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	p := params(t)
	p.CgroupPath = cgroup
	p.Split = SplitParams{Mode: mode, Subnets: prefixes(t, subnets), ForceDNS: forceDNS}
	tx := NewTx(func(store.PrevState) error { return nil })
	if err := Configure(tx, p); err != nil {
		t.Fatal(err)
	}
	counters := `table inet yggtest {
	chain post { type filter hook postrouting priority 200; policy accept;
		oifname "lan" counter comment "direct"
		oifname "yggtun0" counter comment "tunnel"
		oifname "yggtun0" ip saddr 192.0.2.10 counter comment "tunnel-src4"
		oifname "yggtun0" ip6 saddr 2001:db8::10 counter comment "tunnel-src6"
		oifname "lan" ip saddr 192.0.2.10 counter comment "leak4"
	}
}`
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(counters)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nft: %v\n%s", err, out)
	}
	return tx
}

func prefixes(t *testing.T, in []string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, s := range in {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func counter(t *testing.T, comment string) int {
	t.Helper()
	out, _ := exec.Command("nft", "list", "table", "inet", "yggtest").CombinedOutput()
	m := regexp.MustCompile(`counter packets (\d+) bytes \d+ comment "` + comment + `"`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("no counter %q in:\n%s", comment, out)
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// route sends one UDP packet to dst and says where it left: "direct", "tunnel" or "none".
func route(t *testing.T, dst string) string {
	t.Helper()
	d0, t0 := counter(t, "direct"), counter(t, "tunnel")
	if c, err := net.Dial("udp", dst); err == nil {
		c.Write([]byte("x"))
		c.Close()
	}
	d1, t1 := counter(t, "direct"), counter(t, "tunnel")
	switch {
	case t1 > t0 && d1 == d0:
		return "tunnel"
	case d1 > d0 && t1 == t0:
		return "direct"
	}
	return "none"
}

func TestModeAllUnchanged(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "all", nil, otherCgroup(t))
	defer tx.Rollback()
	if got := route(t, "203.0.113.9:9"); got != "tunnel" {
		t.Fatalf("mode all: %s", got)
	}
	if out, _ := exec.Command("nft", "list", "table", "inet", "yggtunnel").CombinedOutput(); strings.Contains(string(out), "split") || strings.Contains(string(out), "names4") {
		t.Fatalf("mode all must not add split chains or sets:\n%s", out)
	}
}

func TestExcludeSubnetGoesDirect(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "exclude", []string{"203.0.113.0/24"}, otherCgroup(t))
	defer tx.Rollback()
	if got := route(t, "203.0.113.9:9"); got != "direct" {
		t.Fatalf("listed: %s", got)
	}
	if got := route(t, "198.51.100.9:9"); got != "tunnel" {
		t.Fatalf("unlisted: %s", got)
	}
}

func TestExcludeIPv6SubnetGoesDirect(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "exclude", []string{"2001:db8:77::/64"}, otherCgroup(t))
	defer tx.Rollback()
	if got := route(t, "[2001:db8:77::5]:9"); got != "direct" {
		t.Fatalf("listed v6: %s", got)
	}
	if got := route(t, "[2001:db8:99::1]:9"); got != "tunnel" {
		t.Fatalf("unlisted v6: %s", got)
	}
}

func TestExcludeNamesGoDirect(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "exclude", nil, otherCgroup(t))
	defer tx.Rollback()
	if err := UpdateNames([]netip.Addr{netip.MustParseAddr("192.0.2.55")}); err != nil {
		t.Fatal(err)
	}
	if got := route(t, "192.0.2.55:9"); got != "direct" {
		t.Fatalf("resolved name: %s", got)
	}
	if err := UpdateNames(nil); err != nil {
		t.Fatal(err)
	}
	if got := route(t, "192.0.2.55:9"); got != "tunnel" {
		t.Fatalf("after clearing: %s", got)
	}
}

func TestOnlySubnetGoesThroughTunnel(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "only", []string{"203.0.113.0/24"}, otherCgroup(t))
	defer tx.Rollback()
	if got := route(t, "203.0.113.9:9"); got != "tunnel" {
		t.Fatalf("listed: %s", got)
	}
	if got := route(t, "198.51.100.9:9"); got != "direct" {
		t.Fatalf("unlisted: %s", got)
	}
}

func TestOnlyIPv6SubnetGoesThroughTunnel(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "only", []string{"2001:db8:77::/64"}, otherCgroup(t))
	defer tx.Rollback()
	if got := route(t, "[2001:db8:77::5]:9"); got != "tunnel" {
		t.Fatalf("listed v6: %s", got)
	}
}

func TestOnlyNamesGoThroughTunnel(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "only", nil, otherCgroup(t))
	defer tx.Rollback()
	if got := route(t, "192.0.2.55:9"); got != "direct" {
		t.Fatalf("before: %s", got)
	}
	if err := UpdateNames([]netip.Addr{netip.MustParseAddr("192.0.2.55")}); err != nil {
		t.Fatal(err)
	}
	if got := route(t, "192.0.2.55:9"); got != "tunnel" {
		t.Fatalf("resolved name: %s", got)
	}
}

func TestOnlyDaemonTrafficStaysDirect(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "only", []string{"203.0.113.0/24"}, nstest.OwnCgroup(t)) // the test process is "the daemon"
	defer tx.Rollback()
	if got := route(t, "203.0.113.9:9"); got != "direct" {
		t.Fatalf("the daemon's own traffic to a listed address must not enter the tunnel: %s", got)
	}
}

func TestOnlyDNSServersAreForced(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "only", nil, otherCgroup(t))
	defer tx.Rollback()
	for _, dst := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
		if got := route(t, dst); got != "tunnel" {
			t.Fatalf("%s: %s", dst, got)
		}
	}
}

func TestOnlyTunnelTrafficCarriesTunnelSourceAddress(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "only", []string{"203.0.113.0/24", "2001:db8:77::/64"}, otherCgroup(t))
	defer tx.Rollback()
	route(t, "203.0.113.9:9")
	route(t, "[2001:db8:77::5]:9")
	if n := counter(t, "tunnel-src4"); n != 1 {
		t.Fatalf("IPv4: %d packets left with the tunnel address, want 1 (the server would drop the rest)", n)
	}
	if n := counter(t, "tunnel-src6"); n != 1 {
		t.Fatalf("IPv6: %d packets left with the tunnel address, want 1", n)
	}
}

func TestOnlyHasNoSuppressRule(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "only", nil, otherCgroup(t))
	defer tx.Rollback()
	for _, fam := range []string{"-4", "-6"} {
		out, _ := exec.Command("ip", fam, "rule", "show").CombinedOutput()
		s := string(out)
		if !strings.Contains(s, "fwmark 0x5968 lookup 51871") || strings.Contains(s, "suppress_prefixlength") || strings.Contains(s, "not from all fwmark") {
			t.Fatalf("ip %s rule:\n%s", fam, s)
		}
	}
}

func TestSplitRollbackAndRecoverLeaveNoTrace(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	for _, mode := range []string{"exclude", "only"} {
		before := nstest.Snapshot(t)
		var saved store.PrevState
		f, err := CreateTun("yggtun0")
		if err != nil {
			t.Fatal(err)
		}
		tx := NewTx(func(p store.PrevState) error { saved = p; return nil })
		p := params(t)
		p.CgroupPath = otherCgroup(t)
		p.Split = SplitParams{Mode: mode, Subnets: prefixes(t, []string{"203.0.113.0/24"})}
		if err := Configure(tx, p); err != nil {
			t.Fatal(err)
		}
		if err := UpdateNames([]netip.Addr{netip.MustParseAddr("192.0.2.55")}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if after := nstest.Snapshot(t); after != before {
			t.Fatalf("%s rollback left changes\nbefore:\n%s\nafter:\n%s", mode, before, after)
		}
		// and the crash path: only the persisted record survives
		f, _ = CreateTun("yggtun0")
		tx = NewTx(func(p store.PrevState) error { saved = p; return nil })
		if err := Configure(tx, p); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if err := RecoverFrom(saved); err != nil {
			t.Fatal(err)
		}
		if after := nstest.Snapshot(t); after != before {
			t.Fatalf("%s recovery left changes\nbefore:\n%s\nafter:\n%s", mode, before, after)
		}
	}
}

func TestUpdateNamesIsAtomicAndBounded(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "exclude", nil, otherCgroup(t))
	defer tx.Rollback()
	var big []netip.Addr
	for i := 0; i < 3000; i++ {
		big = append(big, netip.AddrFrom4([4]byte{192, 0, byte(2 + i/250), byte(i % 250)}))
	}
	if err := UpdateNames(big); err != nil {
		t.Fatalf("3000 addresses: %v", err)
	}
	if got := route(t, "192.0.2.7:9"); got != "direct" {
		t.Fatalf("an element of the big batch: %s", got)
	}
	splitFlush = func(*nftables.Conn) error { return errors.New("injected netlink failure") }
	defer func() { splitFlush = func(c *nftables.Conn) error { return c.Flush() } }()
	if err := UpdateNames([]netip.Addr{netip.MustParseAddr("198.51.100.77")}); err == nil {
		t.Fatal("expected the injected failure")
	}
	splitFlush = func(c *nftables.Conn) error { return c.Flush() }
	if got := route(t, "192.0.2.7:9"); got != "direct" {
		t.Fatalf("a failed update must keep the old elements: %s", got)
	}
}

func TestUpdateNamesWithoutSplitIsAnError(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "all", nil, otherCgroup(t))
	defer tx.Rollback()
	if err := UpdateNames([]netip.Addr{netip.MustParseAddr("192.0.2.1")}); err == nil {
		t.Fatal("expected an error: mode all has no sets")
	}
}

func TestUpdateSubnetsLive(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "exclude", []string{"203.0.113.0/24"}, otherCgroup(t))
	defer tx.Rollback()
	if got := route(t, "198.51.100.9:9"); got != "tunnel" {
		t.Fatalf("before: %s", got)
	}
	if err := UpdateSubnets(SplitParams{Mode: "exclude", Subnets: prefixes(t, []string{"198.51.100.0/24"})}); err != nil {
		t.Fatal(err)
	}
	if got := route(t, "198.51.100.9:9"); got != "direct" {
		t.Fatalf("new entry: %s", got)
	}
	if got := route(t, "203.0.113.9:9"); got != "tunnel" {
		t.Fatalf("removed entry: %s", got)
	}
}

// ---- a flow keeps the path it started on ---------------------------------------------------------

// flow is one connected UDP socket; send() writes a packet and says where it left.
func flow(t *testing.T, dst string) func() string {
	t.Helper()
	c, err := net.Dial("udp", dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return func() string {
		d0, t0 := counter(t, "direct"), counter(t, "tunnel")
		c.Write([]byte("x"))
		d1, t1 := counter(t, "direct"), counter(t, "tunnel")
		switch {
		case t1 > t0 && d1 == d0:
			return "tunnel"
		case d1 > d0 && t1 == t0:
			return "direct"
		}
		return "none"
	}
}

func TestOnlyFlowStaysInTunnelWhenNameIsRemoved(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "only", nil, otherCgroup(t))
	defer tx.Rollback()
	addr := netip.MustParseAddr("192.0.2.55")
	if err := UpdateNames([]netip.Addr{addr}); err != nil {
		t.Fatal(err)
	}
	send := flow(t, "192.0.2.55:9")
	if got := send(); got != "tunnel" {
		t.Fatalf("first packet: %s", got)
	}
	if err := UpdateNames(nil); err != nil { // the name moved away, or the list changed
		t.Fatal(err)
	}
	if got := send(); got != "tunnel" {
		t.Fatalf("second packet of the SAME flow: %s (it would leave the physical link with the tunnel's source address)", got)
	}
	if n := counter(t, "leak4"); n != 0 {
		t.Fatalf("%d packets left the physical link with the tunnel source address", n)
	}
}

func TestExcludeFlowStaysInTunnelWhenNameIsAdded(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "exclude", nil, otherCgroup(t))
	defer tx.Rollback()
	send := flow(t, "192.0.2.55:9")
	if got := send(); got != "tunnel" {
		t.Fatalf("first packet: %s", got)
	}
	if err := UpdateNames([]netip.Addr{netip.MustParseAddr("192.0.2.55")}); err != nil {
		t.Fatal(err)
	}
	if got := send(); got != "tunnel" {
		t.Fatalf("second packet of the SAME flow: %s", got)
	}
	if n := counter(t, "leak4"); n != 0 {
		t.Fatalf("%d packets left the physical link with the tunnel source address", n)
	}
	if got := route(t, "192.0.2.55:9"); got != "direct" { // a NEW flow follows the list
		t.Fatalf("a new flow: %s", got)
	}
}

func TestOnlyFlowStaysDirectWhenNameIsAdded(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "only", nil, otherCgroup(t))
	defer tx.Rollback()
	send := flow(t, "192.0.2.55:9")
	if got := send(); got != "direct" {
		t.Fatalf("first packet: %s", got)
	}
	if err := UpdateNames([]netip.Addr{netip.MustParseAddr("192.0.2.55")}); err != nil {
		t.Fatal(err)
	}
	if got := send(); got != "direct" {
		t.Fatalf("second packet of the SAME flow: %s", got)
	}
}

func TestExcludeFlowStaysDirectWhenNameIsRemoved(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "exclude", nil, otherCgroup(t))
	defer tx.Rollback()
	UpdateNames([]netip.Addr{netip.MustParseAddr("192.0.2.55")})
	send := flow(t, "192.0.2.55:9")
	if got := send(); got != "direct" {
		t.Fatalf("first packet: %s", got)
	}
	UpdateNames(nil)
	if got := send(); got != "direct" {
		t.Fatalf("second packet of the SAME flow: %s", got)
	}
}

func TestSourceGuardDropsTunnelAddressOnPhysicalLink(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "exclude", []string{"203.0.113.0/24"}, otherCgroup(t))
	defer tx.Rollback()
	// a program that binds the tunnel's address but is forced out of the physical link
	d := net.Dialer{LocalAddr: &net.UDPAddr{IP: net.ParseIP("192.0.2.10")}}
	c, err := d.Dial("udp", "198.51.100.9:9")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	bindToDevice(c, "lan")
	c.Write([]byte("x"))
	if n := counter(t, "leak4"); n != 0 {
		t.Fatalf("%d packets left the physical link with the tunnel source address", n)
	}
	out, _ := exec.Command("nft", "list", "table", "inet", "yggtunnel").CombinedOutput()
	if !regexp.MustCompile(`counter packets [1-9]\d* bytes \d+ drop comment "yggtunnel-src-guard"`).Match(out) {
		t.Fatalf("the source guard did not count a drop:\n%s", out)
	}
}

func TestExcludedTrafficIsNotCaughtByTheSourceGuard(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnv(t, "exclude", []string{"203.0.113.0/24"}, otherCgroup(t))
	defer tx.Rollback()
	if got := route(t, "203.0.113.9:9"); got != "direct" { // masqueraded to the physical address before the guard
		t.Fatalf("an excluded destination: %s", got)
	}
}

func TestOnlyWithoutDomainsDoesNotForceDNS(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnvOpt(t, "only", []string{"203.0.113.0/24"}, otherCgroup(t), false)
	defer tx.Rollback()
	for _, dst := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
		if got := route(t, dst); got != "direct" {
			t.Fatalf("%s: %s — without listed names nothing needs the tunnel's DNS, so these addresses stay direct", dst, got)
		}
	}
}

func TestUpdateSubnetsCanSwitchDNSForcingOn(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := splitEnvOpt(t, "only", nil, otherCgroup(t), false)
	defer tx.Rollback()
	if err := UpdateSubnets(SplitParams{Mode: "only", ForceDNS: true}); err != nil {
		t.Fatal(err)
	}
	if got := route(t, "1.1.1.1:53"); got != "tunnel" {
		t.Fatalf("after listing a domain: %s", got)
	}
}
