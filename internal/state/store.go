// Package state persists Work Items in state.json inside the state
// directory, and guards that directory with a lock so only one Tick (CLI or
// tray app) runs at a time.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

const (
	stateFile = "state.json"
	tempFile  = "state.json.tmp"
	lockFile  = "state.lock"
	version   = 1
)

// ErrLocked means another Tick holds the state directory.
var ErrLocked = errors.New("state directory is locked by another Tick")

// file is the on-disk shape of state.json.
type file struct {
	Version int                 `json:"version"`
	Items   []workflow.WorkItem `json:"items"`
}

// Store is an open, locked state directory. Only a Store can write state.
type Store struct {
	dir  string
	lock *os.File
}

// Open creates the state directory if needed and locks it. It fails fast
// with ErrLocked when another Tick holds the lock; the lock is released by
// Close, or by the OS if the process dies.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock, err := lockDir(filepath.Join(dir, lockFile))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return &Store{dir: dir, lock: lock}, nil
}

// Close releases the lock.
func (s *Store) Close() error { return s.lock.Close() }

// Load reads the current state.
func (s *Store) Load() (workflow.State, error) { return Read(s.dir) }

// Save atomically replaces state.json: it writes a temp file, syncs it, and
// renames it over state.json, so a crash leaves either the old or the new
// state, never a torn one.
func (s *Store) Save(st workflow.State) error {
	data, err := json.MarshalIndent(file{Version: version, Items: st.Items}, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, tempFile)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, stateFile))
}

// Read loads state.json from dir without locking or writing anything; a
// missing file is an empty state. It is safe beside a running Tick because
// Save replaces the file atomically.
func Read(dir string) (workflow.State, error) {
	path := filepath.Join(dir, stateFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return workflow.State{}, nil
	}
	if err != nil {
		return workflow.State{}, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return workflow.State{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Version != version {
		return workflow.State{}, fmt.Errorf("%s: unsupported version %d", path, f.Version)
	}
	return workflow.State{Items: f.Items}, nil
}
