// Command work-agent-tray is the Wails v3 system-tray app (ADR-0004): it runs
// the Tick loop in the background and shows the overall status in the tray.
// It embeds the same core as the headless CLI.
//
// Build it as a GUI-subsystem binary on Windows:
//
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
	t, err := prepare(ctx, *configPath)
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
	daemon   *core.Daemon
	cfg      config.Config
	stateDir string
	log      *logging.Logger
	closers  []io.Closer
}

// prepare makes this the only tray app instance for the config's state
// directory, then loads the config and starts the core. It fails with
// state.ErrAlreadyRunning when another instance is running.
func prepare(ctx context.Context, configPath string) (t *tray, err error) {
	// The state directory holds config.json, state.json and logs/.
	stateDir := filepath.Dir(configPath)
	instance, err := state.LockInstance(stateDir)
	if err != nil {
		return nil, err
	}
	t = &tray{stateDir: stateDir, closers: []io.Closer{instance}}
	defer func() {
		if err != nil {
			t.close()
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
	t.daemon, err = core.Start(ctx, t.cfg, t.log, process.DefaultSearchDirs())
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
	app := application.New(application.Options{
		Name:        "Work Agent",
		Description: "Watches GitHub for the Operator and Wakes Claude Code in Orca Workspaces",
		Assets:      application.AlphaAssets,
		Mac: application.MacOptions{
			// A tray-only app: no Dock icon.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
	})

	icon := app.SystemTray.New()
	icon.SetIcon(trayIcon(core.StatusOK))
	icon.SetTooltip("Work Agent: starting")

	// The loop reports statuses without ever blocking on the UI; only the
	// latest one matters.
	statuses := make(chan core.Status, 1)
	loop := core.NewLoop(t.stateDir, t.cfg.PollInterval(), t.daemon, t.log, func(s core.Status) {
		select {
		case <-statuses:
		default:
		}
		statuses <- s
	})

	loopCtx, stopLoop := context.WithCancel(ctx)
	menu := app.NewMenu()
	menu.Add("Pause all").OnClick(func(*application.Context) {
		go func() {
			if err := loop.PauseAll(loopCtx); err != nil {
				t.log.Error("pause all failed", "err", err)
			}
		}()
	})
	menu.Add("Tick now").OnClick(func(*application.Context) { loop.TickNow() })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	icon.SetMenu(menu)

	loopDone := make(chan struct{})
	var started atomic.Bool
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if started.Swap(true) {
			return
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
