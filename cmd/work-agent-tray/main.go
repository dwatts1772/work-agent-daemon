// Command work-agent-tray is the Wails v3 system-tray app (ADR-0004): it runs
// the Tick loop in the background, shows the overall status in the tray and
// the Work Items in a status window, and delivers desktop notifications
// unless notify.desktop is false.
// It embeds the same core as the headless CLI.
//
// Build the status window's frontend first, then the binary — as a
// GUI-subsystem binary on Windows:
//
//	npm --prefix cmd/work-agent-tray/frontend ci
//	npm --prefix cmd/work-agent-tray/frontend run build
//	go build -ldflags -H=windowsgui ./cmd/work-agent-tray
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/core"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/process"
	"github.com/dwatts1772/work-agent-daemon/internal/state"
)

func main() {
	os.Exit(run())
}

func run() int {
	defaultConfig, err := config.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "work-agent-tray:", err)
		return 1
	}
	configPath := flag.String("config", defaultConfig, "path to config.json")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t, err := prepare(ctx, *configPath, process.DefaultSearchDirs())
	if err != nil {
		fmt.Fprintln(os.Stderr, "work-agent-tray: refusing to start:", err)
		return 1
	}
	defer t.close()
	if err := t.serve(ctx); err != nil {
		t.log.Error("tray app failed", "err", err)
		return 1
	}
	return 0
}

// tray is a started, single-instance tray app that has not yet shown its
// icon.
type tray struct {
	daemon     *core.Daemon
	cfg        config.Config
	configPath string // absolute
	stateDir   string
	log        *logging.Logger
	closers    []io.Closer
}

// prepare makes this the only tray app instance for the config's state
// directory, then loads the config and starts the core. It fails with
// state.ErrAlreadyRunning when another instance is running. Binaries that are
// not configured are looked up on PATH, then in searchDirs.
func prepare(ctx context.Context, configPath string, searchDirs []string) (t *tray, err error) {
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}
	// The state directory holds config.json, state.json and logs/.
	stateDir := filepath.Dir(configPath)
	instance, err := state.LockInstance(stateDir)
	if err != nil {
		return nil, err
	}
	t = &tray{configPath: configPath, stateDir: stateDir, closers: []io.Closer{instance}}
	// Close through a copy: returning nil, err sets t to nil before this runs.
	started := t
	defer func() {
		if err != nil {
			started.close()
		}
	}()

	logFile, err := logging.OpenFile(stateDir)
	if err != nil {
		return nil, err
	}
	t.closers = append(t.closers, logFile)
	t.log = logging.New(os.Stderr, logFile)

	t.cfg, err = config.Load(configPath)
	if err != nil {
		t.log.Error("refusing to start", "err", err)
		return nil, err
	}
	t.daemon, err = core.Start(ctx, t.cfg, t.log, searchDirs)
	if err != nil {
		t.log.Error("refusing to start", "err", err)
		return nil, errors.New(t.log.Redact(err.Error()))
	}
	return t, nil
}

// close releases the single-instance lock and the log.
func (t *tray) close() {
	for i := len(t.closers) - 1; i >= 0; i-- {
		t.closers[i].Close()
	}
}

// serve shows the tray icon and runs the Tick loop until the Operator quits.
// Quitting cancels the loop and waits for any in-flight Tick to end.
func (t *tray) serve(ctx context.Context) error {
	// The loop reports statuses without ever blocking on the UI; only the
	// latest one matters. Each report also refreshes the status window.
	statuses := make(chan core.Status, 1)
	loopCtx, stopLoop := context.WithCancel(ctx)
	var app *application.App
	loop, window := t.statusLoop(loopCtx, statuses, func() { app.Event.Emit(tickEvent) })

	services := []application.Service{application.NewService(window)}
	notifier := newDesktop(t.cfg, t.log)
	if notifier != nil {
		services = append(services, application.NewService(notifier))
		t.daemon.AddNotifier(notifier)
	}
	app = application.New(application.Options{
		Name:        "Work Agent",
		Description: "Watches GitHub for the Operator and Wakes Claude Code in Orca Workspaces",
		Assets:      application.AssetOptions{Handler: frontendHandler(t.log)},
		Services:    services,
		Mac: application.MacOptions{
			// A tray-only app: no Dock icon.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
	})

	// The status window is created hidden and only hidden again on close,
	// so reopening it is instant and quitting stays in the tray menu.
	statusWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Work Agent",
		Width:  1200,
		Height: 600,
		Hidden: true,
		URL:    "/",
	})
	statusWindow.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		statusWindow.Hide()
		e.Cancel()
	})
	openWindow := func() {
		statusWindow.Show()
		statusWindow.Focus()
	}

	icon := app.SystemTray.New()
	icon.SetIcon(trayIcon(core.StatusOK))
	icon.SetTooltip("Work Agent: starting")
	icon.OnClick(openWindow)

	menu := app.NewMenu()
	menu.Add("Open status window").OnClick(func(*application.Context) { openWindow() })
	menu.Add("Pause all").OnClick(func(*application.Context) {
		go func() {
			if err := loop.PauseAll(loopCtx); err != nil {
				t.log.Error("pause all failed", "err", err)
			}
		}()
	})
	menu.Add("Tick now").OnClick(func(*application.Context) { loop.TickNow() })
	t.addStartAtLogin(menu, app.Autostart)
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	icon.SetMenu(menu)

	loopDone := make(chan struct{})
	var started atomic.Bool
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if started.Swap(true) {
			return
		}
		if notifier != nil {
			go notifier.authorize()
		}
		go func() {
			defer close(loopDone)
			loop.Run(loopCtx)
		}()
		go func() {
			var shown core.Status
			for {
				select {
				case <-loopCtx.Done():
					return
				case s := <-statuses:
					if s != shown {
						icon.SetIcon(trayIcon(s))
						shown = s
					}
					icon.SetTooltip(statusTooltips[s])
				}
			}
		}()
	})
	app.OnShutdown(func() {
		stopLoop()
		if started.Load() {
			<-loopDone
		}
		t.log.Info("tray app quit")
	})

	t.log.Info("tray app started", "pollInterval", t.cfg.PollInterval().String())
	return app.Run()
}
