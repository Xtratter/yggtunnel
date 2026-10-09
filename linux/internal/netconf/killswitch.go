package netconf

import (
	"net/netip"

	"github.com/Xtratter/yggtunnel/linux/internal/store"
	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"golang.org/x/sys/unix"
)

const (
	ksTable   = "yggtunnelks"
	ksComment = "yggtunnel-ks-drop"
)

// KSParams describes what the kill switch lets through besides the tunnel.
type KSParams struct {
	IfName   string // the tunnel interface
	Mark     uint32 // the daemon's own traffic carries this mark (see addMark)
	AllowLAN bool   // private, link-local and multicast destinations
}

var (
	lanV4 = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "224.0.0.0/4"}
	lanV6 = []string{"fe80::/10", "fc00::/7", "ff00::/8"}
)

// AddKillSwitch drops every locally generated packet that is not going through the tunnel, is not
// the daemon's own, and is not DHCP, neighbour discovery or (when allowed) local-network traffic.
// A table of the same name left by an earlier run is replaced.
func AddKillSwitch(tx *Tx, p KSParams) error {
	args := map[string]string{"table": ksTable}
	return tx.Do(store.Step{Kind: "killswitch", Args: args},
		func() error {
			if err := delMarkTable(ksTable); err != nil { // stale table from an earlier run
				return err
			}
			c := &nftables.Conn{}
			t := c.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: ksTable})
			ch := c.AddChain(&nftables.Chain{Name: "output", Table: t, Type: nftables.ChainTypeFilter,
				Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityFilter})
			add := func(comment string, exprs ...expr.Any) {
				r := &nftables.Rule{Table: t, Chain: ch, Exprs: exprs}
				if comment != "" {
					r.UserData = userdata.AppendString(nil, userdata.TypeComment, comment)
				}
				c.AddRule(r)
			}
			accept := &expr.Verdict{Kind: expr.VerdictAccept}
			add("", &expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifnameData("lo")}, accept)
			add("", &expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifnameData(p.IfName)}, accept)
			add("", &expr.Meta{Key: expr.MetaKeyMARK, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(p.Mark)}, accept)
			add("", udpPorts(68, 67)...)                // DHCPv4 client
			add("", udpPorts(546, 547)...)              // DHCPv6 client
			for _, typ := range []byte{133, 135, 136} { // router solicitation, neighbour solicitation and advertisement
				add("", &expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_ICMPV6}},
					&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 1},
					&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{typ}}, accept)
			}
			if p.AllowLAN {
				for _, pfx := range lanV4 {
					add("", destPrefix(netip.MustParsePrefix(pfx))...)
				}
				for _, pfx := range lanV6 {
					add("", destPrefix(netip.MustParsePrefix(pfx))...)
				}
			}
			add(ksComment, &expr.Counter{}, &expr.Verdict{Kind: expr.VerdictDrop})
			return c.Flush()
		},
		func() error { return delMarkTable(ksTable) })
}

// udpPorts matches UDP with the given source and destination ports and accepts.
func udpPorts(sport, dport uint16) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_UDP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(sport)},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(dport)},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}
}

// destPrefix accepts packets whose destination address is inside pfx (IPv4 or IPv6).
func destPrefix(pfx netip.Prefix) []expr.Any {
	proto, offset, size := byte(unix.NFPROTO_IPV4), uint32(16), uint32(4)
	if pfx.Addr().Is6() {
		proto, offset, size = unix.NFPROTO_IPV6, 24, 16
	}
	mask := make([]byte, size)
	for i := 0; i < pfx.Bits(); i++ {
		mask[i/8] |= 0x80 >> (i % 8)
	}
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: size},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: size, Mask: mask, Xor: make([]byte, size)},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: pfx.Masked().Addr().AsSlice()},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}
}

// KillSwitchActive reports whether the kill switch table exists.
func KillSwitchActive() bool {
	c := &nftables.Conn{}
	tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return false
	}
	for _, t := range tables {
		if t.Name == ksTable {
			return true
		}
	}
	return false
}
