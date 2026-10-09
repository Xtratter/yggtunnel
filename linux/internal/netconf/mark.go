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

// addMark marks every locally generated packet of the cgroup, so that `ip rule` sends it around the tunnel.
func addMark(tx *Tx, cgroupPath string, mark uint32) error {
	id, level, err := cgroupID(cgroupPath)
	if err != nil {
		return fmt.Errorf("cgroup %s: %w", cgroupPath, err)
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
			return c.Flush()
		},
		func() error { return delMarkTable(markTable) })
}

func delMarkTable(name string) error {
	c := &nftables.Conn{}
	c.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: name})
	if err := c.Flush(); err != nil && !gone(err) {
		return err
	}
	return nil
}
