package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSbEscapeBlocksLineAndAttributeInjection(t *testing.T) {
	in := "Stocks\nClick me | bash=/bin/sh param1=-c param2=id terminal=false\r--sub"
	got := sbEscape(in)
	if strings.ContainsAny(got, "|\n\r") {
		t.Fatalf("sbEscape left a separator in %q", got)
	}
	if !strings.Contains(got, "¦ bash=") {
		t.Fatalf("expected pipe to be replaced, got %q", got)
	}
}

func TestValidCadence(t *testing.T) {
	for _, ok := range []string{"10m", "1s", "30s", "1h", "2d"} {
		if !validCadence(ok) {
			t.Errorf("validCadence(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "0m", "10", "m", "10m/../x", "../../x", "10m.sh", "1 m", "10M"} {
		if validCadence(bad) {
			t.Errorf("validCadence(%q) = true, want false", bad)
		}
	}
}

func TestShellQuoteRoundTrips(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	for _, s := range []string{
		"/usr/local/bin/indw",
		"/Users/a b/bin/indw",
		`/tmp/$(touch pwned)/it's "quoted"/` + "`id`",
	} {
		out, err := exec.Command("bash", "-c", "printf %s "+shellQuote(s)).Output()
		if err != nil {
			t.Fatalf("bash: %v", err)
		}
		if string(out) != s {
			t.Errorf("shellQuote(%q) round-tripped to %q", s, out)
		}
	}
}

func TestPadRightCountsRunes(t *testing.T) {
	if got := padRight("₹ab", 5); got != "₹ab  " {
		t.Errorf("padRight = %q", got)
	}
}
