package main

import (
	"context"
	"embed"
	"io/fs"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dwatts1772/work-agent-daemon/internal/core"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// frontend is the status window's built frontend; see main.go for how to
// build it.
//
//go:embed all:frontend/dist
var frontend embed.FS

// frontendHandler serves the built frontend. A binary built without it
// still runs the Tick loop; its window only says how to build the frontend.
func frontendHandler(log *logging.Logger) http.Handler {
	dist, err := fs.Sub(frontend, "frontend/dist")
	if err == nil {
		_, err = fs.Stat(dist, "index.html")
	}
	if err != nil {
		const msg = "The status window's frontend was not built into this binary: run `npm run build` in cmd/work-agent-tray/frontend, then rebuild."
		log.Error("status window unavailable", "err", msg)
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, msg, http.StatusServiceUnavailable)
		})
	}
	return application.AssetFileServerFS(dist)
}

// tickEvent is the Wails event that tells the status window to refresh.
const tickEvent = "work-agent:tick"

// statusLoop returns the Tick loop and the status window behind it. After
// every Tick attempt the loop reports the overall status to statuses, keeping
// only the latest, and asks the window to refresh.
func (t *tray) statusLoop(ctx context.Context, statuses chan core.Status, refresh func()) (*core.Loop, *statusWindow) {
	loop := core.NewLoop(t.stateDir, t.cfg.PollInterval(), t.daemon, t.log, func(s core.Status) {
		select {
		case <-statuses:
		default:
		}
		statuses <- s
		refresh()
	})
	return loop, &statusWindow{ctx: ctx, stateDir: t.stateDir, loop: loop, daemon: t.daemon}
}

// statusWindow is the Wails service the status window calls. Its pause,
// resume and wake run the same core commands as the CLI, between Ticks and
// under the state-directory lock.
type statusWindow struct {
	ctx      context.Context
	stateDir string
	loop     *core.Loop
	daemon   *core.Daemon
}

// Items lists every Work Item. Like `work-agent list` it reads state without
// taking the lock, so it works beside a running Tick.
func (s *statusWindow) Items() ([]core.ItemView, error) {
	st, err := state.Read(s.stateDir)
	if err != nil {
		return nil, err
	}
	items := make([]core.ItemView, 0, len(st.Items))
	for _, w := range st.Items {
		items = append(items, core.View(w))
	}
	return items, nil
}

// Pause runs `work-agent pause` on item id.
func (s *statusWindow) Pause(id string) (core.ItemView, error) {
	return s.do(func(store *state.Store) (workflow.WorkItem, error) { return core.Pause(store, id) })
}

// Resume runs `work-agent resume` on item id.
func (s *statusWindow) Resume(id string) (core.ItemView, error) {
	return s.do(func(store *state.Store) (workflow.WorkItem, error) { return core.Resume(store, id) })
}

// Wake runs `work-agent wake` on item id.
func (s *statusWindow) Wake(id string) (core.ItemView, error) {
	return s.do(func(store *state.Store) (workflow.WorkItem, error) { return s.daemon.Wake(s.ctx, store, id) })
}

func (s *statusWindow) do(command func(*state.Store) (workflow.WorkItem, error)) (core.ItemView, error) {
	var w workflow.WorkItem
	err := s.loop.Do(s.ctx, func(store *state.Store) error {
		var err error
		w, err = command(store)
		return err
	})
	return core.View(w), err
}
