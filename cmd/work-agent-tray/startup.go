package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// loginItemID names the tray app's login item (macOS) or startup entry
// (Windows Run key value).
const loginItemID = "com.dwatts1772.work-agent"

// autostart is the part of Wails' AutostartManager the tray app uses. It
// registers a macOS login item (SMAppService, or a LaunchAgent that is never
// kept alive) or a Windows Run key entry — never a service supervisor
// (ADR-0004).
type autostart interface {
	EnableWithOptions(application.AutostartOptions) error
	Disable() error
	IsEnabled() (bool, error)
}

// addStartAtLogin adds the "Start at login" toggle to the tray menu.
func (t *tray) addStartAtLogin(menu *application.Menu, a autostart) {
	on, err := a.IsEnabled()
	if err != nil {
		t.log.Error("reading start at login failed", "err", err)
	}
	item := menu.AddCheckbox("Start at login", on)
	item.OnClick(func(*application.Context) {
		// The checkbox has already flipped to what the Operator wants.
		want := item.Checked()
		if err := t.setStartAtLogin(a, want); err != nil {
			t.log.Error("changing start at login failed", "on", want, "err", err)
			item.SetChecked(!want)
			return
		}
		t.log.Info("start at login changed", "on", want)
	})
}

// setStartAtLogin registers or removes the login item. It starts this
// binary by absolute path with this config's absolute path, since a login
// item gets a minimal PATH and another working directory. (Wails ignores the
// arguments for a bundled .app on macOS 13+, which registers through
// SMAppService; the tray app is not bundled, so it gets a LaunchAgent.)
func (t *tray) setStartAtLogin(a autostart, on bool) error {
	if !on {
		return a.Disable()
	}
	return a.EnableWithOptions(application.AutostartOptions{
		Identifier: loginItemID,
		Arguments:  []string{"--config", t.configPath},
	})
}
