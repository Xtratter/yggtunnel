package netconf

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"

	"github.com/Xtratter/yggtunnel/linux/internal/store"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Params describes the tunnel interface and how traffic reaches it.
type Params struct {
	IfName     string
	YggAddr    netip.Addr // the node's Yggdrasil address, added as /7
	ClientIP4  netip.Addr // added as /32
	ClientIP6  netip.Addr // added as /128; the zero Addr means the server has no IPv6
	MTU        int
	Table      int    // routing table holding the default routes through the tunnel
	Mark       uint32 // traffic of CgroupPath carries this mark and bypasses the tunnel
	CgroupPath string // absolute cgroup v2 directory of the daemon
	Split      SplitParams
}

// gone reports "nothing to undo" errors, so undo can run twice.
func gone(err error) bool {
	for _, e := range []error{unix.ENOENT, unix.ESRCH, unix.ENODEV, unix.EADDRNOTAVAIL} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// CreateTun opens a TUN device named name (no packet info header). A link of that name left by an
// earlier crash is removed first.
func CreateTun(name string) (*os.File, error) {
	if l, err := netlink.LinkByName(name); err == nil {
		if err := netlink.LinkDel(l); err != nil && !gone(err) {
			return nil, fmt.Errorf("remove leftover %s: %w", name, err)
		}
	}
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w", err)
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("create %s: %w", name, err)
	}
	return os.NewFile(uintptr(fd), "/dev/net/tun"), nil
}

func delLink(name string) error {
	l, err := netlink.LinkByName(name)
	if err != nil {
		return nil // already gone
	}
	if err := netlink.LinkDel(l); err != nil && !gone(err) {
		return err
	}
	return nil
}

func addAddr(tx *Tx, l netlink.Link, pfx netip.Prefix) error {
	ipn := &net.IPNet{IP: pfx.Addr().AsSlice(), Mask: net.CIDRMask(pfx.Bits(), pfx.Addr().BitLen())}
	name := l.Attrs().Name
	args := map[string]string{"if": name, "cidr": pfx.String()}
	return tx.Do(store.Step{Kind: "addr", Args: args},
		func() error { return netlink.AddrAdd(l, &netlink.Addr{IPNet: ipn}) },
		func() error { return delAddr(args) })
}

func delAddr(a map[string]string) error {
	l, err := netlink.LinkByName(a["if"])
	if err != nil {
		return nil // the link is gone, and its addresses with it
	}
	pfx, err := netip.ParsePrefix(a["cidr"])
	if err != nil {
		return err
	}
	ipn := &net.IPNet{IP: pfx.Addr().AsSlice(), Mask: net.CIDRMask(pfx.Bits(), pfx.Addr().BitLen())}
	if err := netlink.AddrDel(l, &netlink.Addr{IPNet: ipn}); err != nil && !gone(err) {
		return err
	}
	return nil
}

func itoa(i int) string { return strconv.Itoa(i) }
