package netconf

import (
	"errors"
	"net/netip"

	"github.com/Xtratter/yggtunnel/linux/internal/store"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Configure turns the TUN device p.IfName (already created by CreateTun) into the tunnel interface:
// addresses, MTU, default routes in p.Table, ip rules and the traffic mark. Every step goes through tx.
func Configure(tx *Tx, p Params) error {
	if p.CgroupPath == "" {
		return errors.New("cgroup path is required: without it the daemon's own packets would loop through the tunnel")
	}
	l, err := netlink.LinkByName(p.IfName)
	if err != nil {
		return err
	}
	if err := tx.Do(store.Step{Kind: "link", Args: map[string]string{"name": p.IfName}},
		func() error { return nil }, // the link exists already; the step only records how to remove it
		func() error { return delLink(p.IfName) }); err != nil {
		return err
	}
	pfx := []netip.Prefix{netip.PrefixFrom(p.YggAddr, 7), netip.PrefixFrom(p.ClientIP4, 32)}
	if p.ClientIP6.IsValid() {
		pfx = append(pfx, netip.PrefixFrom(p.ClientIP6, 128))
	}
	for _, x := range pfx {
		if err := addAddr(tx, l, x); err != nil {
			return err
		}
	}
	if err := netlink.LinkSetMTU(l, p.MTU); err != nil {
		return err
	}
	if err := netlink.LinkSetUp(l); err != nil {
		return err
	}
	// ::/0 is always routed into the tunnel (also without a client IPv6 address), so IPv6 cannot leak around it.
	for _, fam := range []int{unix.AF_INET, unix.AF_INET6} {
		if err := addDefaultRoute(tx, l, p.Table, fam); err != nil {
			return err
		}
		if err := addRules(tx, p.Table, p.Mark, fam); err != nil {
			return err
		}
	}
	return addMark(tx, p.CgroupPath, p.Mark)
}
