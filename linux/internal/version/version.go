// Package version holds the Linux client version.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var raw string

// String is the version as MAJOR.MINOR.PATCH.
func String() string { return strings.TrimSpace(raw) }
