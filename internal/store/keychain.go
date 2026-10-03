package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/oauth"
)

// Keychain stores tokens in macOS login keychain via the `security` CLI.
// Service: indmoney-watch, Account: tokens.
//
// The vars are non-const so tests can point at a scratch service name and
// avoid clobbering a real user session in the login keychain.
var (
	kcService = "indmoney-watch"
	kcAccount = "tokens"
)

// securityBin is invoked by absolute path: it receives the token bundle on
// stdin, so a `security` shim earlier in $PATH must never be picked up.
const securityBin = "/usr/bin/security"

// SaveTokens writes the token bundle to Keychain via `security -i` with the
// secret encoded as a hex string passed to `-X`. Two reasons:
//
//  1. argv exposure — without `-i`, the only way to set a password is
//     `add-generic-password -w SECRET …`, putting SECRET on argv where any
//     user's `ps` can briefly see it. `-i` reads sub-commands from stdin, so
//     `-w SECRET` becomes a stdin token, not a process argument. (Same trick
//     as `gh` CLI / zalando/go-keyring.)
//
//  2. line/quote handling — `security -i` is line-oriented and its quoting
//     rules don't match a POSIX shell: embedded newlines terminate the
//     command, and backslash escapes are not processed inside double quotes.
//     A token can legitimately contain any byte, so we encode it as hex and
//     pass it via `-X`, which only ever sees `[0-9a-f]`.
func SaveTokens(t *oauth.Tokens) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	hexBlob := hex.EncodeToString(b)
	// `security -i` accepts one command per line on stdin and exits on EOF.
	// -U updates the item in place if it already exists, so there's no
	// delete-then-add window in which a failed add would leave no tokens.
	// Service/account are fixed constants (no user input), so quoting them is
	// unnecessary, but we keep them double-quoted for defense in depth.
	cmds := fmt.Sprintf(
		"add-generic-password -s %q -a %q -U -X %s\n",
		kcService, kcAccount, hexBlob,
	)
	cmd := exec.Command(securityBin, "-i")
	cmd.Stdin = strings.NewReader(cmds)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Don't include `out` in errors — it may echo our input, which
		// carries the hex-encoded tokens, and errors end up in agent.log.
		return fmt.Errorf("keychain save: %w", err)
	}
	// `security -i` exits 0 even when a sub-command fails; detect that from
	// the output, again without echoing it.
	if strings.Contains(string(out), "add-generic-password:") &&
		strings.Contains(strings.ToLower(string(out)), "error") {
		return fmt.Errorf("keychain save: add-generic-password failed")
	}
	return nil
}

func LoadTokens() (*oauth.Tokens, error) {
	cmd := exec.Command(securityBin, "find-generic-password",
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
