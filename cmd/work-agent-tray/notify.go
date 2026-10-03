package main

import (
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/notify"
)

// desktop is the Notifier that delivers native desktop notifications
// (Windows toast / macOS Notification Center) through Wails.
type desktop struct {
	service *notifications.NotificationService
}

// newDesktop returns the desktop Notifier, or nil when the config turns
// desktop notifications off.
func newDesktop(cfg config.Config) *desktop {
	if !cfg.DesktopNotifications() {
		return nil
	}
	return &desktop{service: notifications.New()}
}

// Notify shows n. Its ID is the condition, so on macOS a new occurrence
// replaces the previous one's banner rather than piling up.
func (d *desktop) Notify(n notify.Notification) error {
	return d.service.SendNotification(notifications.NotificationOptions{
		ID:    n.Key(),
		Title: n.Title,
		Body:  n.Body,
	})
}
