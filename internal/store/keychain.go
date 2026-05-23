package store

import (
	"context"
	"encoding/hex"
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
//
// The vars are non-const so tests can point at a scratch service name and
// avoid clobbering a real user session in the login keychain.
var (
	kcService = "indmoney-watch"
	kcAccount = "tokens"
)

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
	// Delete first (security errors on duplicate without -U) then add with
	// -U so an upgrade path also works. Service/account are fixed constants
	// (no user input), so quoting them is unnecessary, but we keep them
	// double-quoted for defense in depth in case the constants ever change.
	cmds := fmt.Sprintf(
		"delete-generic-password -s %q -a %q\nadd-generic-password -s %q -a %q -U -X %s\n",
		kcService, kcAccount,
		kcService, kcAccount, hexBlob,
	)
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(cmds)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Don't include `out` in errors — it may echo our argv.
		// security -i prints "SecKeychainItemCreateFromContent ... -25299" if
		// the delete failed because the item didn't exist; that's fine and
		// the subsequent add-generic-password with -U will succeed. We only
		// fail here if the *exec itself* failed.
		return fmt.Errorf("keychain save: %w", err)
	}
	// Treat unknown-error lines from the add (not the pre-delete) as failure.
	if strings.Contains(string(out), "add-generic-password:") &&
		strings.Contains(string(out), "error") {
		return fmt.Errorf("keychain save: %s", strings.TrimSpace(string(out)))
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
