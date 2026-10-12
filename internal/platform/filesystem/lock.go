package filesystem

import (
	"os"
	"path/filepath"
	"time"
)

// LockFile takes an exclusive lock on path (created if missing), waiting up
// to wait for another process to release it. unlock releases it. ok=false
// means it could not be taken in time; callers then carry on without it,
// so a stuck process never blocks git for good. The lock goes away by
// itself when the process exits.
func LockFile(path string, wait time.Duration) (unlock func(), ok bool) {
	none := func() {}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return none, false
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return none, false
	}
	deadline := time.Now().Add(wait)
	for !tryLock(f) {
		if time.Now().After(deadline) {
			f.Close()
			return none, false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return func() { unlockFile(f); f.Close() }, true
}
