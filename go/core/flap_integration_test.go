package core

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Watches a real peer for a while and logs every up/down change, skipped unless YGG_WATCH_PEER is set:
// YGG_WATCH_PEER=wss://host:443/path YGG_WATCH_SECONDS=90 go test -run WatchPeer -v .
func TestWatchPeer(t *testing.T) {
	uri := os.Getenv("YGG_WATCH_PEER")
	if uri == "" {
		t.Skip("YGG_WATCH_PEER not set")
	}
	secs := 90
	if s := os.Getenv("YGG_WATCH_SECONDS"); s != "" {
		json.Unmarshal([]byte(s), &secs)
	}
	cfg, _ := GenerateConfig()
	if _, err := node.Start(cfg, []string{uri}); err != nil {
		t.Fatal(err)
	}
	defer node.Stop()
	start, last, lastErr := time.Now(), false, ""
	for time.Since(start) < time.Duration(secs)*time.Second {
		var st status
		json.Unmarshal([]byte(node.Status()), &st)
		up, e, upt := false, "", 0.0
		if len(st.Peers) > 0 {
			up, e, upt = st.Peers[0].Up, st.Peers[0].Error, st.Peers[0].UptimeS
		}
		if up != last || e != lastErr {
			t.Logf("%5.1fs up=%v uptime=%.1f err=%s", time.Since(start).Seconds(), up, upt, e)
			last, lastErr = up, e
		}
		time.Sleep(250 * time.Millisecond)
	}
}
