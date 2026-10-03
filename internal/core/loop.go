package core

import (
	"context"
	"errors"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// Ticker is what the Loop drives; *Daemon is one.
type Ticker interface {
	Tick(ctx context.Context, store *state.Store) (Result, error)
	OrcaAvailable(ctx context.Context) bool
}

// Loop runs Ticks in the background for the tray app: one on start, then
// one every interval or on TickNow. It holds the state-directory lock only
// for the duration of each Tick, so the CLI can Tick in between, and it
// skips a Tick while the CLI holds the lock.
type Loop struct {
	stateDir string
	interval time.Duration
	ticker   Ticker
	log      *logging.Logger
	onStatus func(Status)
	now      chan struct{}
	commands chan command
}

// command is an Operator command waiting for the Loop to run it.
type command struct {
	run   func(*state.Store) error
	reply chan error
}

// NewLoop returns a Loop that reports the overall status to onStatus after
// every Tick attempt.
func NewLoop(stateDir string, interval time.Duration, ticker Ticker, log *logging.Logger, onStatus func(Status)) *Loop {
	return &Loop{
		stateDir: stateDir,
		interval: interval,
		ticker:   ticker,
		log:      log,
		onStatus: onStatus,
		now:      make(chan struct{}, 1),
		commands: make(chan command),
	}
}

// TickNow asks for a Tick without waiting for the interval. Requests made
// while a Tick is pending coalesce into one.
func (l *Loop) TickNow() {
	select {
	case l.now <- struct{}{}:
	default:
	}
}

// PauseAll Pauses every Work Item by the Operator, between Ticks and under
// the lock. It fails with state.ErrLocked while the CLI holds the lock.
func (l *Loop) PauseAll(ctx context.Context) error {
	return l.Do(ctx, l.pauseEverything)
}

// Do runs an Operator command between Ticks, holding the lock on the state
// directory for it. It fails with state.ErrLocked, without running it, while
// the CLI holds the lock.
func (l *Loop) Do(ctx context.Context, run func(*state.Store) error) error {
	c := command{run: run, reply: make(chan error, 1)}
	select {
	case l.commands <- c:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-c.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run Ticks until ctx is cancelled, then returns once any in-flight Tick
// has ended and released the lock.
func (l *Loop) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-l.commands:
			c.reply <- l.locked(c.run)
			continue
		case <-timer.C:
		case <-l.now:
		}
		if ctx.Err() != nil {
			return
		}
		l.tick(ctx)
		timer.Reset(l.interval)
	}
}

// tick runs one Tick under the lock and reports the resulting status.
func (l *Loop) tick(ctx context.Context) {
	signals := Signals{}
	store, err := state.Open(l.stateDir)
	switch {
	case errors.Is(err, state.ErrLocked):
		l.log.Info("tick skipped: the state directory is locked by another Tick")
	case err != nil:
		l.log.Error("tick failed", "err", err)
		signals.TickFailed = true
	default:
		_, err = l.ticker.Tick(ctx, store)
		store.Close()
		if err != nil && ctx.Err() == nil {
			l.log.Error("tick failed", "err", l.log.Redact(err.Error()))
			signals.TickFailed = true
		}
	}
	if ctx.Err() != nil {
		return
	}
	signals.OrcaAvailable = l.ticker.OrcaAvailable(ctx)
	l.onStatus(OverallStatus(signals))
}

// locked runs run holding the lock on the state directory.
func (l *Loop) locked(run func(*state.Store) error) error {
	store, err := state.Open(l.stateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	return run(store)
}

func (l *Loop) pauseEverything(store *state.Store) error {
	current, err := store.Load()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	next := current
	changed := false
	for _, w := range current.Items {
		var actions []workflow.Action
		next, actions, _ = workflow.PauseByOperator(next, w.ID, now)
		changed = changed || len(actions) > 0
	}
	if !changed {
		return nil
	}
	if err := store.Save(next); err != nil {
		return err
	}
	l.log.Info("paused all Work Items", "items", len(next.Items))
	return nil
}
