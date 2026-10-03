package main

import (
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
)

func TestDesktopNotificationsFollowTheConfig(t *testing.T) {
	off := false
	disabled := config.Config{Notify: config.Notify{Desktop: &off}}
	if d := newDesktop(disabled); d != nil {
		t.Errorf("notify.desktop: false still made a desktop notifier")
	}
	if d := newDesktop(config.Config{}); d == nil || d.service == nil {
		t.Errorf("desktop notifications are on by default, got no desktop notifier")
	}
}
