package basics

import "testing"

// This file says `package basics`, not `package basics_test`, so it is
// inside your package and can see unexported names. Nothing outside the
// package can.

func TestLP_1_UnexportedGreeting(t *testing.T) {
	if greeting != "hello" {
		t.Errorf("the unexported constant greeting should be %q, got %q",
			"hello", greeting)
	}
	if got := Greet("x"); got != greeting+", x" {
		t.Errorf("Greet should be built from greeting: Greet(%q) = %q, "+
			"want %q", "x", got, greeting+", x")
	}
}
