// Package auth asks polkit whether the process on the other end of the socket may change the network.
package auth

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Xtratter/yggtunnel/linux/internal/ipc"
	"github.com/godbus/dbus/v5"
)

// ActionConnect covers connect, disconnect, panic and profile import.
const ActionConnect = "io.github.xtratter.yggtunnel.connect"

// Authorizer decides whether a peer may run a state-changing command.
type Authorizer interface {
	Check(peer ipc.Peer, action string) error
}

// Subject is polkit's unix-process subject; StartTime guards against PID reuse.
type Subject struct {
	PID       uint32
	StartTime uint64
}

type polkit struct {
	call func(s Subject, action string) (bool, error)
}

func newPolkit(call func(Subject, string) (bool, error)) *polkit { return &polkit{call: call} }

// NewPolkit connects to polkit on the system bus.
func NewPolkit() (Authorizer, error) {
	bus, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("polkit: %w", err)
	}
	obj := bus.Object("org.freedesktop.PolicyKit1", "/org/freedesktop/PolicyKit1/Authority")
	return newPolkit(func(s Subject, action string) (bool, error) {
		type subject struct {
			Kind    string
			Details map[string]dbus.Variant
		}
		subj := subject{"unix-process", map[string]dbus.Variant{
			"pid": dbus.MakeVariant(s.PID), "start-time": dbus.MakeVariant(s.StartTime)}}
		var authorized, challenge bool
		var details map[string]string
		err := obj.Call("org.freedesktop.PolicyKit1.Authority.CheckAuthorization", 0,
			subj, action, map[string]string{}, uint32(0), "").Store(&authorized, &challenge, &details)
		return authorized, err
	}), nil
}

// Check refuses on any doubt: unknown peer, vanished process, polkit error.
func (p *polkit) Check(peer ipc.Peer, action string) error {
	if peer.PID <= 0 {
		return errors.New("not authorized: the calling process is unknown")
	}
	start, err := startTime(peer.PID)
	if err != nil {
		return fmt.Errorf("not authorized: %w", err)
	}
	ok, err := p.call(Subject{PID: uint32(peer.PID), StartTime: start}, action)
	if err != nil {
		return fmt.Errorf("not authorized: polkit: %w", err)
	}
	if !ok {
		return errors.New("not authorized by polkit")
	}
	return nil
}

// startTime is the process start time in clock ticks since boot (field 22 of /proc/PID/stat).
func startTime(pid int) (uint64, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, fmt.Errorf("process %d is gone", pid)
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')') // the command name may contain spaces and parentheses
	if i < 0 {
		return 0, errors.New("bad /proc stat")
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return 0, errors.New("short /proc stat")
	}
	return strconv.ParseUint(f[19], 10, 64)
}
