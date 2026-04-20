package dispatch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock acquires an exclusive, non-blocking flock on lockPath. Returns a
// release function and ok=true on success, or ok=false if another process
// holds the lock.
func Lock(lockPath string) (release func(), ok bool, err error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, false, fmt.Errorf("mkdir lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("open lock: %w", err)
	}
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, true, nil
}
