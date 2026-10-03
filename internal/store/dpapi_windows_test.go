package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/oauth"
)

func TestDPAPIRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tokens.dpapi")
	orig := tokensFile
	tokensFile = func() (string, error) { return p, nil }
	t.Cleanup(func() { tokensFile = orig })

	in := &oauth.Tokens{
		AccessToken:  "access-\"$`'\\\n" + string(bytes.Repeat([]byte("x"), 4096)),
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		ClientID:     "cid",
		ClientSecret: "secret",
	}
	if err := SaveTokens(in); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("refresh")) || bytes.Contains(raw, []byte("secret")) {
		t.Fatal("token file contains plaintext")
	}
	out, err := LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if *out != *in {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}

	// Decrypting without our entropy must fail.
	if _, err := dpapiUnprotectWithEntropy(raw, nil); err == nil {
		t.Fatal("decrypted without entropy")
	}
}
