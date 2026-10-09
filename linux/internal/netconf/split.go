package netconf

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"golang.org/x/sys/unix"
)

// ForceMark selects the tunnel table in split mode "only" (the bypass mark is Params.Mark).
const ForceMark = 0x5968

const (
	defaultBypassMark = 0x5967
	// decidedMark is saved in a connection that was looked at and is on neither list, so that it keeps
	// the default path for good. It means nothing to `ip rule` (no rule matches it).
	decidedMark  = 0x5969
	splitChain   = "split"
	namesTimeout = 5 * time.Minute
	setChunk     = 1000 // elements per netlink message
)

// SplitParams says which destinations use the tunnel.
//   - "all" (or ""): everything, the lists are ignored — the scheme of plans 1–3, unchanged;
//   - "exclude": everything except the lists, which are marked to bypass the tunnel;
//   - "only": nothing except the lists, which are marked to use the tunnel.
type SplitParams struct {
	Mode    string
	Subnets []netip.Prefix // already normalised
	Mark    uint32         // the bypass mark; 0 means the default 0x5967 (Configure fills it in)
	// ForceDNS (mode "only"): always send the tunnel's DNS servers into the tunnel. Needed only while names
	// are listed; without it nothing is asked there, and those addresses are left alone.
	ForceDNS bool
}

func (s SplitParams) active() bool { return s.Mode == "exclude" || s.Mode == "only" }

func (s SplitParams) bypass() uint32 {
	if s.Mark == 0 {
		return defaultBypassMark
	}
	return s.Mark
}

// The DNS servers of the tunnel link are always tunnelled in mode "only", or name resolution for
// the listed names would not work.
var forcedDNS = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")}

// splitFlush commits a batch; tests replace it to inject failures.
var splitFlush = func(c *nftables.Conn) error { return c.Flush() }

func setMark(mark uint32) []expr.Any {
	return []expr.Any{
		&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint32(mark)},
		&expr.Meta{Key: expr.MetaKeyMARK, SourceRegister: true, Register: 1},
	}
}

// saveMark copies the packet mark into the connection's mark.
func saveMark() []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyMARK, Register: 1},
		&expr.Ct{Key: expr.CtKeyMARK, SourceRegister: true, Register: 1},
	}
}

// restoreMark copies a saved connection mark back into the packet mark.
func restoreMark() []expr.Any {
	return []expr.Any{
		&expr.Ct{Key: expr.CtKeyMARK, Register: 1},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: make([]byte, 4)},
		&expr.Meta{Key: expr.MetaKeyMARK, SourceRegister: true, Register: 1},
	}
}

// addSourceGuard drops, after source NAT, anything that leaves through another interface with the
// tunnel's own address as its source: the address belongs to the tunnel, and a packet carrying it
// on the physical link is a leak whatever the rules above decided.
func addSourceGuard(c *nftables.Conn, t *nftables.Table, p Params) {
	ch := c.AddChain(&nftables.Chain{Name: "guard", Table: t, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookPostrouting, Priority: nftables.ChainPriorityRef(110)}) // srcnat is 100
	add := func(family byte, offset, size uint32, addr netip.Addr) {
		c.AddRule(&nftables.Rule{Table: t, Chain: ch, UserData: userdata.AppendString(nil, userdata.TypeComment, "yggtunnel-src-guard"), Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{family}},
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1}, &expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: ifnameData(p.IfName)},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: size},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: addr.AsSlice()},
			&expr.Counter{}, &expr.Verdict{Kind: expr.VerdictDrop},
		}})
	}
	if p.ClientIP4.IsValid() {
		add(unix.NFPROTO_IPV4, 12, 4, p.ClientIP4)
	}
	if p.ClientIP6.IsValid() {
		add(unix.NFPROTO_IPV6, 8, 16, p.ClientIP6)
	}
}

// addSplitChain creates the sets, the `split` chain and the jump to it from the `mark` chain.
func addSplitChain(c *nftables.Conn, t *nftables.Table, mark *nftables.Chain, p SplitParams) {
	s4 := &nftables.Set{Table: t, Name: "names4", KeyType: nftables.TypeIPAddr, HasTimeout: true, Timeout: namesTimeout}
	s6 := &nftables.Set{Table: t, Name: "names6", KeyType: nftables.TypeIP6Addr, HasTimeout: true, Timeout: namesTimeout}
	_ = c.AddSet(s4, nil)
	_ = c.AddSet(s6, nil)
	split := c.AddChain(&nftables.Chain{Name: splitChain, Table: t})
	c.AddRule(&nftables.Rule{Table: t, Chain: mark, Exprs: []expr.Any{&expr.Verdict{Kind: expr.VerdictJump, Chain: splitChain}}})
	addSplitRules(c, t, split, p, s4, s6)
}

// addSplitRules fills the `split` chain: the listed subnets and the resolved names get the mark that
// sends them around the tunnel (exclude) or into it (only).
func addSplitRules(c *nftables.Conn, t *nftables.Table, ch *nftables.Chain, p SplitParams, s4, s6 *nftables.Set) {
	bypass := p.bypass()
	action := bypass
	var guard []expr.Any
	if p.Mode == "only" {
		action = ForceMark
		// the daemon's own traffic (already marked by the cgroup rule) must never be pulled into the tunnel
		guard = []expr.Any{&expr.Meta{Key: expr.MetaKeyMARK, Register: 1}, &expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(bypass)}}
	}
	// A connection is classified once, by its first packet that has no saved decision (`ct mark` is 0),
	// and the decision is saved in the connection; the `mark` chain restores it for every later packet.
	// So a list change (a name that moved, an entry added or removed) never moves a running connection to
	// the other path, where it would leave with the wrong source address. (`ct state new` cannot be used:
	// for UDP it stays "new" until the first reply.)
	undecided := []expr.Any{
		&expr.Ct{Key: expr.CtKeyMARK, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: make([]byte, 4)},
	}
	add := func(match []expr.Any) {
		var e []expr.Any
		e = append(e, undecided...)
		e = append(e, guard...)
		e = append(e, match...)
		e = append(e, setMark(action)...)
		e = append(e, saveMark()...)
		c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: e})
	}
	for _, pfx := range p.Subnets {
		add(destMatch(pfx))
	}
	if p.Mode == "only" && p.ForceDNS {
		for _, a := range forcedDNS {
			add(destMatch(netip.PrefixFrom(a, a.BitLen())))
		}
	}
	for _, s := range []struct {
		set          *nftables.Set
		proto        byte
		offset, size uint32
	}{{s4, unix.NFPROTO_IPV4, 16, 4}, {s6, unix.NFPROTO_IPV6, 24, 16}} {
		add([]expr.Any{
			&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{s.proto}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: s.offset, Len: s.size},
			&expr.Lookup{SourceRegister: 1, SetName: s.set.Name, SetID: s.set.ID},
		})
	}
	// everything else that is still undecided keeps the default path
	var e []expr.Any
	e = append(e, undecided...)
	e = append(e, guard...)
	e = append(e,
		&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint32(decidedMark)},
		&expr.Ct{Key: expr.CtKeyMARK, SourceRegister: true, Register: 1})
	c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: e})
}

// snat rewrites the source of packets that carry ForceMark and leave through the tunnel.
func snat(c *nftables.Conn, t *nftables.Table, ch *nftables.Chain, ifName string, family byte, addr netip.Addr) {
	c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyMARK, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(ForceMark)},
		&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifnameData(ifName)},
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{family}},
		&expr.Immediate{Register: 1, Data: addr.AsSlice()},
		&expr.NAT{Type: expr.NATTypeSourceNAT, Family: uint32(family), RegAddrMin: 1},
	}})
}

func markTableRef() *nftables.Table {
	return &nftables.Table{Family: nftables.TableFamilyINet, Name: markTable}
}

// UpdateNames replaces the addresses of the resolved names in ONE batch: if it fails the old
// addresses stay. Entries expire after five minutes unless refreshed. It fails when the tunnel is up
// in mode "all" (there are no sets then) or not up at all.
func UpdateNames(addrs []netip.Addr) error {
	c := &nftables.Conn{}
	t := markTableRef()
	s4, err4 := c.GetSetByName(t, "names4")
	s6, err6 := c.GetSetByName(t, "names6")
	if err4 != nil || err6 != nil {
		return errors.New("split routing is not active: there are no name sets (mode all, or the tunnel is down)")
	}
	var e4, e6 []nftables.SetElement
	seen := map[netip.Addr]bool{}
	for _, a := range addrs {
		a = a.Unmap()
		if !a.IsValid() || seen[a] {
			continue
		}
		seen[a] = true
		el := nftables.SetElement{Key: a.AsSlice(), Timeout: namesTimeout}
		if a.Is4() {
			e4 = append(e4, el)
		} else {
			e6 = append(e6, el)
		}
	}
	c.FlushSet(s4)
	c.FlushSet(s6)
	for _, part := range []struct {
		set   *nftables.Set
		elems []nftables.SetElement
	}{{s4, e4}, {s6, e6}} {
		// one netlink message holds at most 64 KiB, so add in chunks; they still commit together
		for len(part.elems) > 0 {
			n := min(len(part.elems), setChunk)
			if err := c.SetAddElements(part.set, part.elems[:n]); err != nil {
				return fmt.Errorf("%s: %w", part.set.Name, err)
			}
			part.elems = part.elems[n:]
		}
	}
	return splitFlush(c)
}

// UpdateSubnets replaces the subnet rules in ONE batch (the mode itself cannot change while up).
func UpdateSubnets(p SplitParams) error {
	if !p.active() {
		return errors.New("split routing is not active in this mode")
	}
	c := &nftables.Conn{}
	t := markTableRef()
	s4, err4 := c.GetSetByName(t, "names4")
	s6, err6 := c.GetSetByName(t, "names6")
	if err4 != nil || err6 != nil {
		return errors.New("split routing is not active: there are no name sets (the tunnel is down?)")
	}
	ch := &nftables.Chain{Name: splitChain, Table: t}
	c.FlushChain(ch)
	addSplitRules(c, t, ch, p, s4, s6)
	return splitFlush(c)
}
