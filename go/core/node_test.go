package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStartStop(t *testing.T) {
	cfg, err := GenerateConfig()
	if err != nil {
		t.Fatal(err)
	}
	addr, err := node.Start(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(addr, "2") {
		t.Fatalf("not a Yggdrasil address: %s", addr)
	}
	if _, err := node.Start(cfg, nil); err == nil {
		t.Fatal("second Start must fail")
	}
	var s status
	if err := json.Unmarshal([]byte(node.Status()), &s); err != nil {
		t.Fatal(err)
	}
	if !s.Running || s.Address != addr || node.MTU() < 1280 {
		t.Fatalf("bad status %+v mtu %d", s, node.MTU())
	}
	node.Stop()
	if strings.Contains(node.Status(), `"running":true`) {
		t.Fatal("still running after Stop")
	}
	// Same config → same address (the key is persistent).
	addr2, err := node.Start(cfg, nil)
	if err != nil || addr2 != addr {
		t.Fatalf("restart: %v %s != %s", err, addr2, addr)
	}
	node.Stop()
}
