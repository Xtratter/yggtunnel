package core

import "testing"

func TestDefaultNodeIsSingleton(t *testing.T) {
	if Default() == nil {
		t.Fatal("Default() is nil")
	}
	if Default() != Default() {
		t.Fatal("Default() returns different nodes")
	}
}
