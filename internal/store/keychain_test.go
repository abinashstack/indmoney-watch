package store

import (
	"os/exec"
	"testing"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/oauth"
)

// TestKeychainRoundTripAdversarial verifies SaveTokens/LoadTokens survive
// values containing shell metacharacters. The whole point of the `security -i`
// rewrite is that no part of the token ever lands on argv, so the test pushes
// strings that would break naive shell concatenation: single quotes, double
// quotes, backticks, $vars, newlines, backslashes.
//
// Skipped automatically when /usr/bin/security is unavailable (non-macOS / CI).
func TestKeychainRoundTripAdversarial(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("security(1) not available")
	}

	// Use a scratch service name so we don't touch the real session.
	origService, origAccount := kcService, kcAccount
	kcService = "indmoney-watch-test"
	kcAccount = "tokens-test"
	t.Cleanup(func() {
		_ = exec.Command("security", "delete-generic-password",
			"-s", kcService, "-a", kcAccount).Run()
		kcService, kcAccount = origService, origAccount
	})

	adversarial := "tok_\"$`'\\nhello\nworld'`"
	in := &oauth.Tokens{
		AccessToken:  adversarial,
		RefreshToken: "ref_" + adversarial,
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		Scope:        "portfolio:read",
		ClientID:     "cid_" + adversarial,
	}

	if err := SaveTokens(in); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	// Read back the raw stored blob via the CLI to verify we round-tripped
	// the exact JSON we marshalled, not a shell-mangled version.
	raw, err := exec.Command("security", "find-generic-password",
		"-s", kcService, "-a", kcAccount, "-w").Output()
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	t.Logf("raw stored bytes: %q", string(raw))

	out, err := LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if out.AccessToken != in.AccessToken {
		t.Errorf("AccessToken mismatch:\n got=%q\nwant=%q", out.AccessToken, in.AccessToken)
	}
	if out.RefreshToken != in.RefreshToken {
		t.Errorf("RefreshToken mismatch:\n got=%q\nwant=%q", out.RefreshToken, in.RefreshToken)
	}
	if out.ClientID != in.ClientID {
		t.Errorf("ClientID mismatch:\n got=%q\nwant=%q", out.ClientID, in.ClientID)
	}
	if !out.ExpiresAt.Equal(in.ExpiresAt) {
		t.Errorf("ExpiresAt mismatch: got=%v want=%v", out.ExpiresAt, in.ExpiresAt)
	}
}
