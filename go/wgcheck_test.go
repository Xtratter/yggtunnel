package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestWgKeyPairMatchesWgTool(t *testing.T) {
	if _, err := exec.LookPath("wg"); err != nil {
		t.Skip("wg not installed")
	}
	s, _ := WgKeyPair()
	var k map[string]string
	json.Unmarshal([]byte(s), &k)
	cmd := exec.Command("wg", "pubkey")
	cmd.Stdin = strings.NewReader(k["private"])
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != k["public"] {
		t.Fatalf("wg pubkey %q != %q (%v)", out, k["public"], err)
	}
}
