package netconf

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/Xtratter/yggtunnel/linux/internal/store"
	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

const (
	markTable  = "yggtunnel"
	cgroupRoot = "/sys/fs/cgroup"
)

// cgroupID is what the kernel compares in `socket cgroupv2`: the inode number of the cgroup directory.
func cgroupID(path string) (id uint64, level uint32, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, errors.New("no inode number for " + path)
	}
	rel := strings.Trim(strings.TrimPrefix(path, cgroupRoot), "/")
	if rel != "" {
		level = uint32(len(strings.Split(rel, "/")))
	}
	return st.Ino, level, nil
}

// addMark marks every locally generated packet of the cgroup, so that `ip rule` sends it around the
// tunnel. The kernel chooses a socket's source address at connect(), before the packet is marked, so
// a connection opened while the tunnel is up carries the tunnel's address; the nat chain rewrites it
// to the outgoing link's address (wg-quick avoids the problem by setting SO_MARK on its own socket).
func addMark(tx *Tx, p Params) error {
	mark, ifName := p.Mark, p.IfName
	id, level, err := cgroupID(p.CgroupPath)
	if err != nil {
		return fmt.Errorf("cgroup %s: %w", p.CgroupPath, err)
	}
	args := map[string]string{"table": markTable}
	return tx.Do(store.Step{Kind: "nft", Args: args},
		func() error {
			c := &nftables.Conn{}
			t := c.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: markTable})
			ch := c.AddChain(&nftables.Chain{Name: "mark", Table: t, Type: nftables.ChainTypeRoute,
				Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityMangle})
			c.AddRule(&nftables.Rule{Table: t, Chain: ch, Exprs: []expr.Any{
				&expr.Socket{Key: expr.SocketKeyCgroupv2, Level: level, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint64(id)},
				&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint32(mark)},
				&expr.Meta{Key: expr.MetaKeyMARK, SourceRegister: true, Register: 1},
			}})
			if p.Split.active() {
				addSplitChain(c, t, ch, p.Split)
			}
			nat := c.AddChain(&nftables.Chain{Name: "nat", Table: t, Type: nftables.ChainTypeNAT,
				Hooknum: nftables.ChainHookPostrouting, Priority: nftables.ChainPriorityNATSource})
			c.AddRule(&nftables.Rule{Table: t, Chain: nat, Exprs: []expr.Any{
				&expr.Meta{Key: expr.MetaKeyMARK, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(mark)},
				&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
				&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: ifnameData(ifName)},
				&expr.Masq{},
			}})
			if p.Split.Mode == "only" {
				// the source address of a connection is chosen before the packet is marked, from the direct
				// routes; what is sent into the tunnel must carry the tunnel's own address
				snat(c, t, nat, ifName, unix.NFPROTO_IPV4, p.ClientIP4)
				if p.ClientIP6.IsValid() {
					snat(c, t, nat, ifName, unix.NFPROTO_IPV6, p.ClientIP6)
				}
			}
			return c.Flush()
		},
		func() error { return delMarkTable(markTable) })
}

// ifnameData is an interface name as nftables compares it: NUL-padded to IFNAMSIZ.
func ifnameData(name string) []byte {
	b := make([]byte, 16)
	copy(b, name)
	return b
}

func delMarkTable(name string) error {
	c := &nftables.Conn{}
	c.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: name})
	if err := c.Flush(); err != nil && !gone(err) {
		return err
	}
	return nil
}
