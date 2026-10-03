package main

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/notify"
)

// toasts is the part of the Wails notification service the tray app uses.
type toasts interface {
	ServiceStartup(ctx context.Context, options application.ServiceOptions) error
	ServiceShutdown() error
	RequestNotificationAuthorization() (bool, error)
	SendNotification(options notifications.NotificationOptions) error
}

// desktop is the Notifier that delivers native desktop notifications
// (Windows toast / macOS Notification Center) through Wails. It is also the
// Wails service that starts them, so that failing to start them leaves the
// tray app running and notifying through the log only.
type desktop struct {
	service toasts
	log     *logging.Logger
	started atomic.Bool
}

// newDesktop returns the desktop Notifier, or nil when the config turns
// desktop notifications off.
func newDesktop(cfg config.Config, log *logging.Logger) *desktop {
	if !cfg.DesktopNotifications() {
		return nil
	}
	return &desktop{service: notifications.New(), log: log}
}

// ServiceStartup starts desktop notifications. It never fails: Wails would
// stop the whole tray app, Tick loop included.
func (d *desktop) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := d.service.ServiceStartup(ctx, options); err != nil {
		d.log.Error("desktop notifications unavailable; notifying through the log only", "err", err)
		return nil
	}
	d.started.Store(true)
	return nil
}

// ServiceShutdown stops desktop notifications.
func (d *desktop) ServiceShutdown() error {
	if !d.started.Load() {
		return nil
	}
	return d.service.ServiceShutdown()
}

// authorize asks the Operator to allow notifications; it is a no-op except
// on macOS, where it asks once.
func (d *desktop) authorize() {
	if !d.started.Load() {
		return
	}
	if ok, err := d.service.RequestNotificationAuthorization(); !ok {
		d.log.Warn("desktop notifications not authorized", "err", err)
	}
}

// Notify shows n. Once already delivers each condition once per occurrence;
// the ID only lets macOS replace an earlier banner for the same condition.
func (d *desktop) Notify(n notify.Notification) error {
	if !d.started.Load() {
		return errors.New("desktop notifications unavailable")
	}
	return d.service.SendNotification(notifications.NotificationOptions{
		ID:    n.Key(),
		Title: n.Title,
		Body:  n.Body,
	})
}
