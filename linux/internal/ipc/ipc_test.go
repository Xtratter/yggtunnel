package ipc

import (
	"context"
	"errors"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"time"
)

type echo struct{}

func (echo) Handle(_ context.Context, _ Peer, req Request) (any, error) {
	if req.Cmd == "fail" {
		return nil, errors.New("boom")
	}
	return map[string]int{"x": 1}, nil
}

func start(t *testing.T) (*Server, string) {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	g, err := user.LookupGroupId(u.Gid)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "s.sock")
	s, err := Listen(path, g.Name, echo{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestCallRoundTrip(t *testing.T) {
	_, path := start(t)
	c, err := Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out map[string]int
	if err := c.Call("ping", nil, &out); err != nil || out["x"] != 1 {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestHandlerErrorBecomesResponseError(t *testing.T) {
	_, path := start(t)
	c, _ := Dial(path)
	defer c.Close()
	if err := c.Call("fail", nil, nil); err == nil || err.Error() != "boom" {
		t.Fatalf("err=%v", err)
	}
}

func TestBroadcastReachesAllClients(t *testing.T) {
	s, path := start(t)
	var cs []*Client
	for i := 0; i < 2; i++ {
		c, err := Dial(path)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		// a round trip guarantees the server has registered the client
		if err := c.Call("ping", nil, nil); err != nil {
			t.Fatal(err)
		}
		cs = append(cs, c)
	}
	s.Broadcast(Event{Kind: "state", Data: []byte(`"up"`)})
	for _, c := range cs {
		select {
		case ev := <-c.Events():
			if ev.Kind != "state" {
				t.Fatalf("kind %q", ev.Kind)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
		}
	}
}

func TestSocketMode0660(t *testing.T) {
	_, path := start(t)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o660 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestMalformedLineClosesOnlyThatClient(t *testing.T) {
	_, path := start(t)
	bad, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	bad.Write([]byte("not json\n"))
	bad.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := bad.Read(buf); err == nil {
		t.Fatal("malformed client was not closed")
	}
	good, _ := Dial(path)
	defer good.Close()
	if err := good.Call("ping", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPeerCredentialsPassedToHandler(t *testing.T) {
	var got Peer
	h := handlerFunc(func(_ context.Context, p Peer, _ Request) (any, error) { got = p; return nil, nil })
	u, _ := user.Current()
	g, _ := user.LookupGroupId(u.Gid)
	path := filepath.Join(t.TempDir(), "p.sock")
	s, err := Listen(path, g.Name, h)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, _ := Dial(path)
	defer c.Close()
	c.Call("x", nil, nil)
	if got.UID != os.Getuid() || got.PID != os.Getpid() {
		t.Fatalf("peer %+v", got)
	}
}

type handlerFunc func(context.Context, Peer, Request) (any, error)

func (f handlerFunc) Handle(ctx context.Context, p Peer, r Request) (any, error) { return f(ctx, p, r) }
