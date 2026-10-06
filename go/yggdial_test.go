package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/gologme/log"
	"golang.org/x/crypto/ssh"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/ipv6rwc"
)

// testNode is a Yggdrasil node with a stack at [local] and a reader that feeds it.
type testNode struct {
	c   *core.Core
	rwc *ipv6rwc.ReadWriteCloser
	st  *yggStack
}

func newTestNode(t *testing.T, local func(*core.Core) netip.Addr, opts ...core.SetupOption) *testNode {
	t.Helper()
	c, err := core.New(config.GenerateConfig().Certificate, log.New(io.Discard, "", 0), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Stop)
	rwc := ipv6rwc.NewReadWriteCloser(c)
	st, err := newYggStack(rwc, local(c), int(rwc.MTU()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	go func() {
		buf := make([]byte, int(rwc.MTU())+64)
		for {
			k, err := rwc.Read(buf)
			if err != nil {
				return
			}
			st.tap(buf[:k])
		}
	}()
	return &testNode{c, rwc, st}
}

// A command over SSH, from the stack at a subnet address of one node to the stack at the main address of another.
func TestSSHOverYggStack(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	srv := newTestNode(t, func(c *core.Core) netip.Addr { return netip.AddrFrom16([16]byte(c.Address())) },
		core.ListenAddress("tcp://127.0.0.1:"+strconv.Itoa(port)))
	cli := newTestNode(t, subnetAddr, core.Peer{URI: "tcp://127.0.0.1:" + strconv.Itoa(port)})

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	hk, _ := ssh.NewSignerFromKey(priv)
	scfg := &ssh.ServerConfig{NoClientAuth: true}
	scfg.AddHostKey(hk)
	l, err := srv.st.Listen(22)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(c, scfg)
				if err != nil {
					return
				}
				defer sc.Close()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					ch, creqs, _ := nc.Accept()
					go func() {
						for r := range creqs {
							if r.Type == "exec" {
								_ = r.Reply(true, nil)
								_, _ = ch.Write([]byte("ok"))
								_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
								ch.Close()
							}
						}
					}()
				}
			}()
		}
	}()

	target := netip.AddrPortFrom(netip.AddrFrom16([16]byte(srv.c.Address())), 22).String()
	var out []byte
	deadline := time.Now().Add(40 * time.Second) // the nodes need a few seconds to find each other
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, err := cli.st.DialContext(ctx, target)
		cancel()
		if err != nil {
			continue
		}
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		cc, chans, reqs, err := ssh.NewClientConn(conn, target, &ssh.ClientConfig{User: "x", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
		if err != nil {
			conn.Close()
			continue
		}
		sess, err := ssh.NewClient(cc, chans, reqs).NewSession()
		if err != nil {
			t.Fatal(err)
		}
		out, err = sess.Output("true")
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if string(out) != "ok" {
		t.Fatalf("got %q", out)
	}
}
