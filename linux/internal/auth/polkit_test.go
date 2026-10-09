package auth

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Xtratter/yggtunnel/linux/internal/ipc"
)

func TestPolkitSubjectUsesPID(t *testing.T) {
	var got Subject
	var action string
	a := newPolkit(func(s Subject, act string) (bool, error) { got, action = s, act; return true, nil })
	if err := a.Check(ipc.Peer{PID: os.Getpid(), UID: os.Getuid()}, "io.example.act"); err != nil {
		t.Fatal(err)
	}
	if got.PID != uint32(os.Getpid()) || got.StartTime == 0 || action != "io.example.act" {
		t.Fatalf("subject %+v action %q", got, action)
	}
}

func TestCheckDeniedReturnsError(t *testing.T) {
	a := newPolkit(func(Subject, string) (bool, error) { return false, nil })
	err := a.Check(ipc.Peer{PID: os.Getpid()}, "x")
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err=%v", err)
	}
}

func TestCheckBusErrorIsRefusal(t *testing.T) {
	a := newPolkit(func(Subject, string) (bool, error) { return false, errors.New("bus down") })
	if err := a.Check(ipc.Peer{PID: os.Getpid()}, "x"); err == nil {
		t.Fatal("a failing authority must refuse, not allow")
	}
}

func TestUnknownPeerRefusedWithoutAsking(t *testing.T) {
	asked := false
	a := newPolkit(func(Subject, string) (bool, error) { asked = true; return true, nil })
	if err := a.Check(ipc.Peer{PID: -1}, "x"); err == nil || asked {
		t.Fatalf("err=%v asked=%v", err, asked)
	}
}

func TestDeadProcessRefused(t *testing.T) {
	a := newPolkit(func(Subject, string) (bool, error) { return true, nil })
	if err := a.Check(ipc.Peer{PID: 2147483000}, "x"); err == nil {
		t.Fatal("expected error for a process that does not exist")
	}
}
