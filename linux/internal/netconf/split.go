package netconf

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// ForceMark selects the tunnel table in split mode "only" (the bypass mark is Params.Mark).
const ForceMark = 0x5968

const (
	defaultBypassMark = 0x5967
	splitChain        = "split"
	namesTimeout      = 5 * time.Minute
	setChunk          = 1000 // elements per netlink message
)

// SplitParams says which destinations use the tunnel.
//   - "all" (or ""): everything, the lists are ignored — the scheme of plans 1–3, unchanged;
//   - "exclude": everything except the lists, which are marked to bypass the tunnel;
//   - "only": nothing except the lists, which are marked to use the tunnel.
type SplitParams struct {
	Mode    string
	Subnets []netip.Prefix // already normalised
	Mark    uint32         // the bypass mark; 0 means the default 0x5967 (Configure fills it in)
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
	add := func(match []expr.Any) {
		var e []expr.Any
		e = append(e, guard...)
		e = append(e, match...)
		e = append(e, setMark(action)...)
		c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: e})
	}
	for _, pfx := range p.Subnets {
		add(destMatch(pfx))
	}
	if p.Mode == "only" {
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
