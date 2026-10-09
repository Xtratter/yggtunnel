package core

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/gologme/log"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

// Yggdrasil's own ws:// listener, no proxies in between: does the link survive? Skipped unless YGG_WSL_TEST=1.
func TestOwnWSListener(t *testing.T) {
	if os.Getenv("YGG_WSL_TEST") != "1" {
		t.Skip("YGG_WSL_TEST not set")
	}
	c, err := core.New(config.GenerateConfig().Certificate, log.New(os.Stderr, "", 0), core.ListenAddress("ws://127.0.0.1:21470"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	cfg, _ := GenerateConfig()
	if _, err := node.Start(cfg, []string{"ws://127.0.0.1:21470"}); err != nil {
		t.Fatal(err)
	}
	defer node.Stop()
	var upAt time.Time
	for start := time.Now(); time.Since(start) < 30*time.Second; time.Sleep(250 * time.Millisecond) {
		var st status
		json.Unmarshal([]byte(node.Status()), &st)
		up := len(st.Peers) > 0 && st.Peers[0].Up
		if up && upAt.IsZero() {
			upAt = time.Now()
		}
		if !up && !upAt.IsZero() {
			t.Fatalf("dropped after %v: %s", time.Since(upAt).Round(time.Millisecond), st.Peers[0].Error)
		}
	}
	t.Logf("stable for %v", time.Since(upAt).Round(time.Second))
}
