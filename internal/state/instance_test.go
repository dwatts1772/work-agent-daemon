package state

import (
	"errors"
	"testing"
)

func TestASecondTrayAppInstanceRefusesToStart(t *testing.T) {
	dir := t.TempDir()
	first, err := LockInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockInstance(dir); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second LockInstance err = %v, want ErrAlreadyRunning", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := LockInstance(dir)
	if err != nil {
		t.Fatalf("LockInstance after the first instance quit: %v", err)
	}
	again.Close()
}

func TestTheInstanceLockDoesNotBlockTicks(t *testing.T) {
	dir := t.TempDir()
	instance, err := LockInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open while the tray app runs: %v", err)
	}
	store.Close()
}
