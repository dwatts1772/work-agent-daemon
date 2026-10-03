package main

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/core"
)

func TestEveryStatusHasItsOwnTrayIcon(t *testing.T) {
	seen := map[string]core.Status{}
	for _, s := range core.Statuses {
		icon := trayIcon(s)
		if _, err := png.Decode(bytes.NewReader(icon)); err != nil {
			t.Errorf("%s: icon is not a PNG: %v", s, err)
		}
		if other, dup := seen[string(icon)]; dup {
			t.Errorf("%s and %s share an icon", s, other)
		}
		seen[string(icon)] = s
	}
}

func TestTheIconChangesWhenOrcaBecomesUnavailableAndBack(t *testing.T) {
	ok := trayIcon(core.StatusOK)
	down := trayIcon(core.StatusOrcaUnavailable)
	if bytes.Equal(ok, down) {
		t.Fatal("Orca unavailable shows the same icon as ok")
	}
	if !bytes.Equal(trayIcon(core.StatusOK), ok) {
		t.Error("the ok icon is not stable")
	}
}
