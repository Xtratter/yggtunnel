package netconf

import (
	"errors"
	"net"
	"strconv"

	"github.com/Xtratter/yggtunnel/linux/internal/store"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Rule priorities sit just before the main table's own rule (32766), like wg-quick.
const (
	prioSuppress = 32763
	prioTunnel   = 32764
)

func defaultDst(family int) *net.IPNet {
	if family == unix.AF_INET6 {
		return &net.IPNet{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)}
	}
	return &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}
}

func addDefaultRoute(tx *Tx, l netlink.Link, table, family int) error {
	args := map[string]string{"if": l.Attrs().Name, "table": itoa(table), "family": itoa(family)}
	return tx.Do(store.Step{Kind: "route", Args: args},
		func() error {
			err := netlink.RouteAdd(&netlink.Route{LinkIndex: l.Attrs().Index, Dst: defaultDst(family), Table: table, Family: family})
			return ignoreExists(err)
		},
		func() error { return delRoute(args) })
}

func delRoute(a map[string]string) error {
	l, err := netlink.LinkByName(a["if"])
	if err != nil {
		return nil // the routes went with the link
	}
	table, _ := strconv.Atoi(a["table"])
	family, _ := strconv.Atoi(a["family"])
	err = netlink.RouteDel(&netlink.Route{LinkIndex: l.Attrs().Index, Dst: defaultDst(family), Table: table, Family: family})
	if err != nil && !gone(err) {
		return err
	}
	return nil
}

// addRules adds, for one family: «main, ignoring default routes» then «everything not marked
// goes to the tunnel table». Marked traffic (the daemon's own) falls through to the main table.
//
// In split mode "only" the default is the other way round: there is no suppress rule and no catch-all,
// and one rule sends packets carrying ForceMark to the tunnel table.
func addRules(tx *Tx, table int, mark uint32, family int, mode string) error {
	suppress := map[string]string{"family": itoa(family), "prio": itoa(prioSuppress), "table": itoa(unix.RT_TABLE_MAIN), "suppress": "0"}
	tunnel := map[string]string{"family": itoa(family), "prio": itoa(prioTunnel), "table": itoa(table), "mark": itoa(int(mark)), "invert": "1"}
	rules := []map[string]string{suppress, tunnel}
	if mode == "only" {
		force := map[string]string{"family": itoa(family), "prio": itoa(prioTunnel), "table": itoa(table), "mark": itoa(ForceMark), "invert": "0"}
		rules = []map[string]string{force}
	}
	for _, a := range rules {
		a := a
		if err := tx.Do(store.Step{Kind: "rule", Args: a},
			func() error { return ignoreExists(netlink.RuleAdd(ruleOf(a))) },
			func() error { return delRule(a) }); err != nil {
			return err
		}
	}
	return nil
}

// ignoreExists treats "already there" as done: a leftover of an earlier run is exactly what the step
// wants, and its undo removes it.
func ignoreExists(err error) error {
	if errors.Is(err, unix.EEXIST) {
		return nil
	}
	return err
}

func ruleOf(a map[string]string) *netlink.Rule {
	r := netlink.NewRule()
	r.Family, _ = strconv.Atoi(a["family"])
	r.Priority, _ = strconv.Atoi(a["prio"])
	r.Table, _ = strconv.Atoi(a["table"])
	if s, ok := a["suppress"]; ok {
		r.SuppressPrefixlen, _ = strconv.Atoi(s)
	}
	if m, ok := a["mark"]; ok {
		v, _ := strconv.Atoi(m)
		r.Mark = uint32(v)
		r.Invert = a["invert"] == "1"
	}
	return r
}

func delRule(a map[string]string) error {
	if err := netlink.RuleDel(ruleOf(a)); err != nil && !gone(err) {
		return err
	}
	return nil
}
