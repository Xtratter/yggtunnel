package core

import "testing"

func TestSpeedPicks(t *testing.T) {
	rs := []*PeerResult{
		{URI: "a", Done: true, Up: true, RttMs: 120},
		{URI: "b", Done: true, Up: true, RttMs: 80, Loss: 1},
		{URI: "c", Done: true, Up: true, RttMs: 90},
		{URI: "d", Done: true, Up: true, RttMs: -1},          // no route
		{URI: "e", Done: true, Up: false, RttMs: -1},         // no link
		{URI: "f", Done: true, Up: true, RttMs: 40, Loss: 5}, // all lost
		{URI: "g", Done: true, Up: true, RttMs: 60},
	}
	got := ""
	for _, r := range speedPicks(rs, 0) {
		got += r.URI
	}
	if got != "gcab" {
		t.Fatalf("all: %q", got)
	}
	got = ""
	for _, r := range speedPicks(rs, 2) {
		got += r.URI
	}
	if got != "gc" {
		t.Fatalf("best 2: %q", got)
	}
}
