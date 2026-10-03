package store

import (
	"testing"
	"time"
)

// TestLockRefreshExcludesConcurrentHolders checks the refresh lock is
// exclusive: flock locks belong to the open file description, so a second
// open — as in another process — must block until the first releases.
func TestLockRefreshExcludesConcurrentHolders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock, err := lockRefresh()
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	acquired := make(chan func())
	go func() {
		u, err := lockRefresh()
		if err != nil {
			t.Errorf("second lock: %v", err)
			close(acquired)
			return
		}
		acquired <- u
	}()

	select {
	case <-acquired:
		t.Fatal("second holder acquired the lock while the first held it")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case u := <-acquired:
		if u != nil {
			u()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second holder never acquired the lock after release")
	}
}
