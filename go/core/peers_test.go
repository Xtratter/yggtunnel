package core

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gologme/log"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

func TestAutoPick(t *testing.T) {
	pickWarmup, pickInterval = 2*time.Second, time.Second
	var uris []string
	for i := 0; i < 4; i++ {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		uri := "tcp://127.0.0.1:" + strconv.Itoa(port)
		c, err := core.New(config.GenerateConfig().Certificate, log.New(os.Stderr, "", 0), core.ListenAddress(uri))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Stop()
		uris = append(uris, uri)
	}
	dead := "tcp://127.0.0.1:1" // never answers
	cfg, _ := GenerateConfig()
	if _, err := node.StartWith(cfg, append(uris, dead), 2, []string{uris[3]}); err != nil {
		t.Fatal(err)
	}
	defer node.Stop()
	var st status
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(500 * time.Millisecond) {
		json.Unmarshal([]byte(node.Status()), &st)
		up := 0
		for _, p := range st.Peers {
			if p.Up {
				up++
			}
		}
		// 2 fastest + the pinned one stay; one live peer and the dead one go to reserve
		if len(st.Reserve) == 2 && up == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reserve %v, up %d", st.Reserve, up)
		}
	}
	for _, r := range st.Reserve {
		if r == uris[3] {
			t.Fatal("pinned peer parked")
		}
	}
	found := false
	for _, r := range st.Reserve {
		found = found || r == dead
	}
	if !found {
		t.Fatalf("dead peer not parked: %v", st.Reserve)
	}
}

func TestServerOnly(t *testing.T) {
	serverOnlyInterval = 500 * time.Millisecond
	var uris []string
	var cores []*core.Core
	for i := 0; i < 3; i++ {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		uri := "tcp://127.0.0.1:" + strconv.Itoa(port)
		c, err := core.New(config.GenerateConfig().Certificate, log.New(io.Discard, "", 0), core.ListenAddress(uri))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Stop()
		uris, cores = append(uris, uri), append(cores, c)
	}
	// pinned: "our server", with options as the app gives them — Yggdrasil reports links without them
	server := uris[2] + "?priority=1"
	cfg, _ := GenerateConfig()
	if _, err := node.StartWith(cfg, []string{uris[0], uris[1], server}, 3, []string{server}, true); err != nil {
		t.Fatal(err)
	}
	defer node.Stop()
	wait := func(what string, ok func(st status) bool) {
		t.Helper()
		var st status
		for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			json.Unmarshal([]byte(node.Status()), &st)
			if ok(st) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: reserve %v peers %+v", what, st.Reserve, st.Peers)
			}
		}
	}
	// the server is up: both public peers go to reserve, the server stays
	wait("public parked", func(st status) bool { return len(st.Reserve) == 2 })
	// the server goes away: the public peers come back
	cores[2].Stop()
	wait("public back", func(st status) bool {
		up := 0
		for _, p := range st.Peers {
			if p.Up && p.URI != uris[2] {
				up++
			}
		}
		return len(st.Reserve) == 0 && up == 2
	})
}
