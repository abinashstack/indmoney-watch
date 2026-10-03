//go:build !windows

package store

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/abinashstack/indmoney-watch/internal/oauth"
)

// Location describes where tokens are stored, for user-facing messages.
const Location = "macOS Keychain (service: indmoney-watch, account: tokens)"

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
