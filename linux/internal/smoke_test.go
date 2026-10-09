package internal

import (
	"testing"

	"github.com/Xtratter/yggtunnel/go/core"
)

// Proves the replace chain (shared core and the patched ironwood) resolves.
func TestCoreLinks(t *testing.T) {
	cfg, err := core.GenerateConfig()
	if err != nil || cfg == "" {
		t.Fatalf("GenerateConfig: %q, %v", cfg, err)
	}
}
