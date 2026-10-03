//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/abinashstack/indmoney-watch/internal/config"
)

// lockRefresh takes an exclusive, cross-process advisory lock that serialises
// token refreshes. The launchd poller, the SwiftBar plugin and interactive CLI
// commands all run as separate processes against the same Keychain item; if
// two of them redeem the same refresh token concurrently and the server
// rotates refresh tokens, the loser gets invalid_grant and the user is forced
// to log in again. The returned func releases the lock.
func lockRefresh() (func(), error) {
	d, err := config.Dir()
	if err != nil {
		return nil, err
	}
	// Read-only is enough to take the lock, and nothing is ever written to
	// the file, so there is no data a failed Close could lose.
	f, err := os.OpenFile(filepath.Join(d, "refresh.lock"), os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
