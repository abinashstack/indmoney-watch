package store

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/abinashstack/indmoney-watch/internal/config"
)

// lockRefresh takes an exclusive, cross-process lock that serialises token
// refreshes (see lock_unix.go for why). The returned func releases it.
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
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		f.Close()
	}, nil
}
