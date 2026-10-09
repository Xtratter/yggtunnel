package core

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// A real peer check, skipped unless YGG_TEST_PEER is set, e.g.
// YGG_TEST_PEER=wss://example.org/path go test -run Peer -v .
func TestRealPeer(t *testing.T) {
	uri := os.Getenv("YGG_TEST_PEER")
	if uri == "" {
		t.Skip("YGG_TEST_PEER not set")
	}
	cfg, _ := GenerateConfig()
	if _, err := node.Start(cfg, []string{uri}); err != nil {
		t.Fatal(err)
	}
	defer node.Stop()
	start := time.Now()
	for time.Since(start) < 30*time.Second {
		var st status
		json.Unmarshal([]byte(node.Status()), &st)
		if len(st.Peers) > 0 && st.Peers[0].Up {
			t.Logf("up after %v, latency %.0f ms, peer %s", time.Since(start).Round(time.Millisecond), st.Peers[0].LatencyMs, st.Peers[0].Address)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("peer did not come up: %s", node.Status())
}
