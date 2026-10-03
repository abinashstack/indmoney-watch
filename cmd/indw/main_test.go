package main

import (
	"testing"
	"unicode/utf8"
)

func TestTruncIsRuneSafeAndStripsControls(t *testing.T) {
	got := trunc("₹₹₹₹₹₹", 4)
	if !utf8.ValidString(got) || got != "₹₹₹…" {
		t.Errorf("trunc = %q", got)
	}
	if got := trunc("a\x1b]0;pwn\x07b", 40); got != "a ]0;pwn b" {
		t.Errorf("trunc did not strip controls: %q", got)
	}
}
