package state

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const instanceLockFile = "tray.lock"

// ErrAlreadyRunning means another tray app instance holds the state
// directory.
var ErrAlreadyRunning = errors.New("the tray app is already running")

// LockInstance makes the caller the only tray app instance for dir until the
// returned Closer is closed or the process dies. It is separate from the
// Tick lock, so the CLI can still Tick between the tray app's Ticks.
func LockInstance(dir string) (io.Closer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := acquireLock(filepath.Join(dir, instanceLockFile))
	if errors.Is(err, ErrLocked) {
		return nil, fmt.Errorf("%s: %w", dir, ErrAlreadyRunning)
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}
