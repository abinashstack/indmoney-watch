package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/oauth"
)

// Keychain stores tokens in macOS login keychain via the `security` CLI.
// Service: indmoney-watch, Account: tokens.
const (
	kcService = "indmoney-watch"
	kcAccount = "tokens"
)

func SaveTokens(t *oauth.Tokens) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	// Delete first (security CLI errors on duplicate); ignore failure.
	_ = exec.Command("security", "delete-generic-password",
		"-s", kcService, "-a", kcAccount).Run()
	cmd := exec.Command("security", "add-generic-password",
		"-s", kcService,
		"-a", kcAccount,
		"-w", string(b),
		"-U")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("keychain add: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func LoadTokens() (*oauth.Tokens, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", kcService, "-a", kcAccount, "-w")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("keychain find: %w", err)
	}
	var t oauth.Tokens
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &t); err != nil {
		return nil, fmt.Errorf("decode tokens: %w", err)
	}
	return &t, nil
}

// TokenSource implements mcpclient.TokenSource — returns a fresh access token,
// refreshing via the refresh_token grant when within 60 s of expiry.
type TokenSource struct {
	mu     sync.Mutex
	tokens *oauth.Tokens
}

func NewTokenSource() (*TokenSource, error) {
	t, err := LoadTokens()
	if err != nil {
		return nil, err
	}
	return &TokenSource{tokens: t}, nil
}

func (ts *TokenSource) AccessToken(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if time.Until(ts.tokens.ExpiresAt) > 60*time.Second {
		return ts.tokens.AccessToken, nil
	}
	nt, err := oauth.Refresh(ctx, ts.tokens)
	if err != nil {
		return "", fmt.Errorf("refresh: %w", err)
	}
	if err := SaveTokens(nt); err != nil {
		return "", fmt.Errorf("save refreshed: %w", err)
	}
	ts.tokens = nt
	return nt.AccessToken, nil
}
