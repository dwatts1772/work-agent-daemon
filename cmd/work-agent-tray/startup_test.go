package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// loginItems stands in for the OS's login items (Wails' AutostartManager):
// the registered command's arguments by identifier.
type loginItems map[string][]string

func (l loginItems) EnableWithOptions(o application.AutostartOptions) error {
	l[o.Identifier] = o.Arguments
	return nil
}

func (l loginItems) Disable() error {
	clear(l)
	return nil
}

func (l loginItems) IsEnabled() (bool, error) { return len(l) > 0, nil }

func TestTogglingStartAtLoginRegistersThisConfigAndRemovesIt(t *testing.T) {
	path := configFor(t)
	rel, err := filepath.Rel(".", path)
	if err != nil {
		t.Skip("config is on another volume")
	}
	tr, err := prepare(context.Background(), rel, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.close()
	items := loginItems{}

	if err := tr.setStartAtLogin(items, true); err != nil {
		t.Fatal(err)
	}
	// A login item starts with another working directory, so the config is
	// passed by absolute path.
	if got := items[loginItemID]; !slices.Equal(got, []string{"--config", path}) {
		t.Errorf("registered %v, want [--config %s]", items, path)
	}
	if on, _ := startAtLogin(items); !on {
		t.Error("start at login reads as off after turning it on")
	}

	if err := tr.setStartAtLogin(items, false); err != nil {
		t.Fatal(err)
	}
	if on, _ := startAtLogin(items); on {
		t.Errorf("start at login still registered after turning it off: %v", items)
	}
}
