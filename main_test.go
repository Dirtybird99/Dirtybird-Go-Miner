package main

import "testing"

func TestValidSAName(t *testing.T) {
	for _, name := range []string{"v114", "sais"} {
		if !validSAName(name) {
			t.Fatalf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", "SAIS", "libsais", "nope"} {
		if validSAName(name) {
			t.Fatalf("%q should be invalid", name)
		}
	}
}
