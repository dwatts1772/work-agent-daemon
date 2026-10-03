package main

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/notify"
)

func TestDesktopNotificationsFollowTheConfig(t *testing.T) {
	log := logging.New(io.Discard, io.Discard)
	off := false
	disabled := config.Config{Notify: config.Notify{Desktop: &off}}
	if d := newDesktop(disabled, log); d != nil {
		t.Errorf("notify.desktop: false still made a desktop notifier")
	}
	if d := newDesktop(config.Config{}, log); d == nil {
		t.Errorf("desktop notifications are on by default, got no desktop notifier")
	}
}

// fakeToasts stands in for the Wails notification service.
type fakeToasts struct {
	startErr error
	sent     []notifications.NotificationOptions
}

func (f *fakeToasts) ServiceStartup(context.Context, application.ServiceOptions) error {
	return f.startErr
}
func (f *fakeToasts) ServiceShutdown() error                          { return nil }
func (f *fakeToasts) RequestNotificationAuthorization() (bool, error) { return true, nil }
func (f *fakeToasts) SendNotification(o notifications.NotificationOptions) error {
	f.sent = append(f.sent, o)
	return nil
}

var paused = notify.Notification{Kind: notify.Paused, Item: "org/a#1", Title: "Paused: org/a#1", Body: "One"}

func TestANotificationIsShownAsADesktopNotification(t *testing.T) {
	toasts := &fakeToasts{}
	d := &desktop{service: toasts, log: logging.New(io.Discard, io.Discard)}
	if err := d.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := d.Notify(paused); err != nil {
		t.Fatal(err)
	}
	if len(toasts.sent) != 1 || toasts.sent[0].Title != paused.Title || toasts.sent[0].Body != paused.Body {
		t.Fatalf("shown %+v, want one notification titled %q", toasts.sent, paused.Title)
	}
}

func TestTheTrayRunsOnWhenDesktopNotificationsCannotStart(t *testing.T) {
	toasts := &fakeToasts{startErr: errors.New("cannot register the toast activator")}
	d := &desktop{service: toasts, log: logging.New(io.Discard, io.Discard)}

	// An error here would stop the whole tray app, Tick loop included.
	if err := d.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup = %v, want the tray to keep running", err)
	}
	if err := d.Notify(paused); err == nil || len(toasts.sent) != 0 {
		t.Errorf("Notify after a failed startup: err = %v, shown %d; want an error and nothing shown", err, len(toasts.sent))
	}
}
