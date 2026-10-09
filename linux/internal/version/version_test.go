package version

import (
	"regexp"
	"testing"
)

func TestVersionNotEmpty(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(String()) {
		t.Fatalf("bad version %q", String())
	}
}
