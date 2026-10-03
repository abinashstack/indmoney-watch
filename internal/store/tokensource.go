package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/oauth"
)

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
	if err := ts.refreshLocked(ctx); err != nil {
		return "", err
	}
	return ts.tokens.AccessToken, nil
}

// ExpiresAt reports when the current access token expires.
func (ts *TokenSource) ExpiresAt() time.Time {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.tokens.ExpiresAt
}

// ForceRefresh discards the current access token and obtains a new one. Used
// when the server rejects a token we believed to be valid (HTTP 401), e.g.
// because it was revoked early or another process already rotated it.
func (ts *TokenSource) ForceRefresh(ctx context.Context) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	stale := ts.tokens.AccessToken
	ts.tokens.ExpiresAt = time.Time{}
	if err := ts.refreshLocked(ctx); err != nil {
		return err
	}
	if ts.tokens.AccessToken == stale {
		return fmt.Errorf("refresh: server returned the rejected access token")
	}
	return nil
}

// refreshLocked must be called with ts.mu held. It takes the cross-process
// refresh lock, then re-reads Keychain: if another process refreshed while we
// waited, we adopt its tokens instead of redeeming an already-rotated refresh
// token.
func (ts *TokenSource) refreshLocked(ctx context.Context) error {
	unlock, err := lockRefresh()
	if err != nil {
		return fmt.Errorf("refresh lock: %w", err)
	}
	defer unlock()

	if cur, err := LoadTokens(); err == nil &&
		cur.AccessToken != ts.tokens.AccessToken &&
		time.Until(cur.ExpiresAt) > 60*time.Second {
		ts.tokens = cur
		return nil
	} else if err == nil {
		// Same or expired access token: still pick up a newer refresh token
		// if another process stored one.
		ts.tokens.RefreshToken = cur.RefreshToken
	}

	nt, err := oauth.Refresh(ctx, ts.tokens)
	if err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	// The old refresh token may already be invalidated by rotation, so keep
	// the new tokens in memory even if persisting them fails — otherwise this
	// process would be stuck with a dead refresh token.
	ts.tokens = nt
	if err := SaveTokens(nt); err != nil {
		fmt.Fprintf(os.Stderr, "warning: refreshed tokens could not be saved to Keychain: %v\n", err)
	}
	return nil
}
