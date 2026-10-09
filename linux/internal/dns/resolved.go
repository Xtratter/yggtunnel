// Package dns sets the tunnel's DNS servers through systemd-resolved, on the tunnel link only.
// /etc/resolv.conf is never edited.
package dns

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/godbus/dbus/v5"
)

const (
	dest = "org.freedesktop.resolve1"
	path = "/org/freedesktop/resolve1"
	mgr  = "org.freedesktop.resolve1.Manager."
)

// Conn is the thin D-Bus seam used by tests.
type Conn interface {
	Call(method string, args ...any) error
}

// Address is a DNS server as resolved wants it: (iay).
type Address struct {
	Family int32
	Bytes  []byte
}

// Domain is a search or routing domain: (sb).
type Domain struct {
	Name        string
	RoutingOnly bool
}

const (
	afInet  = 2
	afInet6 = 10
)

// Set makes servers the DNS of the link and routes every name to it (routing domain "~.").
func Set(c Conn, ifIndex int, servers []netip.Addr) error {
	if len(servers) == 0 {
		return errors.New("no DNS servers given")
	}
	idx := int32(ifIndex)
	var addrs []Address
	for _, s := range servers {
		a := Address{Family: afInet, Bytes: s.AsSlice()}
		if s.Is6() && !s.Is4In6() {
			a.Family = afInet6
		} else {
			a.Bytes = s.Unmap().AsSlice()
		}
		addrs = append(addrs, a)
	}
	if err := c.Call(mgr+"SetLinkDNS", idx, addrs); err != nil {
		return fmt.Errorf("SetLinkDNS: %w", err)
	}
	if err := c.Call(mgr+"SetLinkDomains", idx, []Domain{{Name: ".", RoutingOnly: true}}); err != nil {
		return fmt.Errorf("SetLinkDomains: %w", err)
	}
	if err := c.Call(mgr+"SetLinkDefaultRoute", idx, true); err != nil {
		return fmt.Errorf("SetLinkDefaultRoute: %w", err)
	}
	return nil
}

// Revert drops everything set on the link.
func Revert(c Conn, ifIndex int) error {
	return c.Call(mgr+"RevertLink", int32(ifIndex))
}

type busConn struct{ obj dbus.BusObject }

func (b busConn) Call(method string, args ...any) error { return b.obj.Call(method, 0, args...).Err }

// SystemConn connects to systemd-resolved over the system bus.
func SystemConn() (Conn, error) {
	bus, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("cannot reach systemd-resolved (system bus: %w)", err)
	}
	var has bool
	if err := bus.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, dest).Store(&has); err != nil || !has {
		bus.Close()
		return nil, errors.New("systemd-resolved is not running: DNS for the tunnel cannot be set")
	}
	return busConn{bus.Object(dest, dbus.ObjectPath(path))}, nil
}
