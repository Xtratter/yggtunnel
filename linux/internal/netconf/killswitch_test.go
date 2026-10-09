package netconf

import (
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Xtratter/yggtunnel/linux/internal/nstest"
	"github.com/Xtratter/yggtunnel/linux/internal/store"
)

// ksEnv builds a namespace with a physical link `lan` (192.168.77.2/24, default via .1), brings the
// tunnel up with the daemon's cgroup set to `cgroup`, and arms the kill switch. The process under
// test is "the daemon" only when `cgroup` is its own cgroup.
func ksEnv(t *testing.T, cgroup string, allowLAN bool) *Tx {
	t.Helper()
	for _, c := range [][]string{
		{"link", "add", "lan", "type", "dummy"},
		{"addr", "add", "192.168.77.2/24", "dev", "lan"},
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
	tx := NewTx(func(store.PrevState) error { return nil })
	p := params(t)
	p.CgroupPath = cgroup
	if err := Configure(tx, p); err != nil {
		t.Fatal(err)
	}
	if err := AddKillSwitch(tx, KSParams{IfName: p.IfName, Mark: p.Mark, AllowLAN: allowLAN}); err != nil {
		t.Fatal(err)
	}
	return tx
}

// otherCgroup is a cgroup directory that is not an ancestor of the test process, so the process is
// "another application". (`socket cgroupv2 level N` matches the cgroup AND everything below it, so
// the root cgroup would match every process.)
func otherCgroup(t *testing.T) string {
	t.Helper()
	own := nstest.OwnCgroup(t)
	entries, err := os.ReadDir("/sys/fs/cgroup")
	if err != nil {
		t.Skip("cannot read /sys/fs/cgroup")
	}
	for _, e := range entries {
		dir := "/sys/fs/cgroup/" + e.Name()
		if e.IsDir() && own != dir && !strings.HasPrefix(own, dir+"/") {
			return dir
		}
	}
	t.Skip("no cgroup outside the test process's own tree")
	return ""
}

// leak removes the tunnel's routes: unmarked traffic now falls through to the main table's default.
func leak(t *testing.T) {
	t.Helper()
	for _, fam := range []string{"-4", "-6"} {
		exec.Command("ip", fam, "route", "flush", "table", "51871").Run()
	}
}

func sendUDP(dst string) {
	c, err := net.Dial("udp", dst)
	if err != nil {
		return
	}
	defer c.Close()
	c.Write([]byte("x")) // an error here is the kernel refusing it: that is what the tests look at via counters
}

var dropCounter = regexp.MustCompile(`counter packets (\d+) bytes \d+ drop`)

func dropped(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("nft", "list", "table", "inet", "yggtunnelks").CombinedOutput()
	if err != nil {
		t.Fatalf("nft list: %v\n%s", err, out)
	}
	m := dropCounter.FindSubmatch(out)
	if m == nil {
		t.Fatalf("no drop counter in:\n%s", out)
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

func TestKillSwitchDropsLeakWhenTunnelRouteVanishes(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := ksEnv(t, otherCgroup(t), false)
	defer tx.Rollback()
	leak(t)
	sendUDP("203.0.113.9:9")
	if n := dropped(t); n != 1 {
		t.Fatalf("drop counter %d, want 1", n)
	}
}

func TestKillSwitchLetsDaemonTrafficOut(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := ksEnv(t, nstest.OwnCgroup(t), false)
	defer tx.Rollback()
	leak(t)
	sendUDP("203.0.113.9:9")
	if n := dropped(t); n != 0 {
		t.Fatalf("the daemon's own traffic was dropped (%d)", n)
	}
}

func TestKillSwitchLetsTunnelTrafficOut(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := ksEnv(t, otherCgroup(t), false)
	defer tx.Rollback()
	sendUDP("203.0.113.9:9") // the tunnel route is intact: it leaves through yggtun0
	if n := dropped(t); n != 0 {
		t.Fatalf("traffic through the tunnel was dropped (%d)", n)
	}
}

func TestKillSwitchLANBlockedWhenNotAllowed(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := ksEnv(t, otherCgroup(t), false)
	defer tx.Rollback()
	sendUDPBound("192.168.77.50:9", "lan") // on-link destination, sent directly
	if n := dropped(t); n != 1 {
		t.Fatalf("drop counter %d, want 1", n)
	}
}

func TestKillSwitchLANAllowed(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := ksEnv(t, otherCgroup(t), true)
	defer tx.Rollback()
	sendUDPBound("192.168.77.50:9", "lan")
	if n := dropped(t); n != 0 {
		t.Fatalf("LAN traffic was dropped although allowed (%d)", n)
	}
}

func TestKillSwitchPublicViaLANStillDroppedWhenLANAllowed(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := ksEnv(t, otherCgroup(t), true)
	defer tx.Rollback()
	leak(t)
	sendUDP("203.0.113.9:9")
	if n := dropped(t); n != 1 {
		t.Fatalf("drop counter %d, want 1", n)
	}
}

func TestKillSwitchLetsDHCPOut(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := ksEnv(t, otherCgroup(t), false)
	defer tx.Rollback()
	sendUDPFromPortBound(68, "192.168.77.1:67", "lan")
	if n := dropped(t); n != 0 {
		t.Fatalf("DHCP was dropped (%d)", n)
	}
}

func TestKillSwitchRollbackRemovesTable(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := ksEnv(t, otherCgroup(t), false)
	if !KillSwitchActive() {
		t.Fatal("not active after AddKillSwitch")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if KillSwitchActive() {
		t.Fatal("still active after rollback")
	}
	if out, _ := exec.Command("nft", "list", "ruleset").CombinedOutput(); strings.Contains(string(out), "yggtunnel") {
		t.Fatalf("rules left behind:\n%s", out)
	}
}

func TestKillSwitchRecoverFromRemovesTable(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	var saved store.PrevState
	for _, c := range [][]string{{"link", "add", "lan", "type", "dummy"}, {"addr", "add", "192.168.77.2/24", "dev", "lan"}, {"link", "set", "lan", "up"}} {
		exec.Command("ip", c...).Run()
	}
	f, err := CreateTun("yggtun0")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tx := NewTx(func(p store.PrevState) error { saved = p; return nil })
	p := params(t)
	if err := Configure(tx, p); err != nil {
		t.Fatal(err)
	}
	if err := AddKillSwitch(tx, KSParams{IfName: p.IfName, Mark: p.Mark}); err != nil {
		t.Fatal(err)
	}
	// the daemon "dies": only the record survives
	if err := RecoverFrom(saved); err != nil {
		t.Fatal(err)
	}
	if KillSwitchActive() {
		t.Fatal("kill switch survived recovery: the user would be locked out")
	}
	if err := RecoverFrom(saved); err != nil {
		t.Fatalf("second recovery: %v", err)
	}
}

func TestKillSwitchTwiceIsIdempotent(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	tx := ksEnv(t, otherCgroup(t), false)
	defer tx.Rollback()
	if err := AddKillSwitch(tx, KSParams{IfName: "yggtun0", Mark: 0x5967, AllowLAN: true}); err != nil {
		t.Fatalf("second AddKillSwitch: %v", err)
	}
	out, _ := exec.Command("nft", "list", "tables").CombinedOutput()
	if strings.Count(string(out), "yggtunnelks") != 1 {
		t.Fatalf("expected one table:\n%s", out)
	}
	// and a table with that name from an earlier run does not block a fresh Tx
	tx2 := NewTx(func(store.PrevState) error { return nil })
	if err := AddKillSwitch(tx2, KSParams{IfName: "yggtun0", Mark: 0x5967}); err != nil {
		t.Fatalf("stale table made AddKillSwitch fail: %v", err)
	}
}

func TestKillSwitchActiveReflectsTable(t *testing.T) {
	if !nstest.InNetns(t) {
		return
	}
	if KillSwitchActive() {
		t.Fatal("active in a fresh namespace")
	}
	tx := ksEnv(t, otherCgroup(t), false)
	if !KillSwitchActive() {
		t.Fatal("not active")
	}
	tx.Rollback()
}

func TestTxUndoRemovesOnlyThatStep(t *testing.T) {
	var ran []string
	var last store.PrevState
	tx := NewTx(func(p store.PrevState) error { last = p; return nil })
	mk := func(kind string) {
		tx.Do(store.Step{Kind: kind}, func() error { return nil }, func() error { ran = append(ran, kind); return nil })
	}
	mk("a")
	mk("killswitch")
	mk("b")
	if err := tx.Undo("killswitch"); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "killswitch" {
		t.Fatalf("ran %v", ran)
	}
	if len(last.Steps) != 2 || last.Steps[0].Kind != "a" || last.Steps[1].Kind != "b" {
		t.Fatalf("record %+v", last.Steps)
	}
	if err := tx.Undo("killswitch"); err != nil || len(ran) != 1 {
		t.Fatalf("second Undo: err=%v ran=%v", err, ran)
	}
	tx.Rollback()
	if strings.Join(ran, ",") != "killswitch,b,a" {
		t.Fatalf("rollback order %v", ran)
	}
}

// sendUDPBound sends from a socket bound to a device, so the packet goes straight out of it
// whatever the routing rules say (a program that does that is exactly what a kill switch must stop).
func sendUDPBound(dst, dev string) {
	c, err := net.Dial("udp", dst)
	if err != nil {
		return
	}
	defer c.Close()
	bindToDevice(c, dev)
	c.Write([]byte("x"))
}

func sendUDPFromPortBound(port int, dst, dev string) {
	d := net.Dialer{LocalAddr: &net.UDPAddr{Port: port}}
	c, err := d.Dial("udp", dst)
	if err != nil {
		return
	}
	defer c.Close()
	bindToDevice(c, dev)
	c.Write([]byte("x"))
}
