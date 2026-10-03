package core

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// fakeTicker counts Ticks and can hold a Tick open until released.
type fakeTicker struct {
	mu       sync.Mutex
	ticks    int
	orcaDown bool
	err      error
	ticked   chan struct{}
	hold     chan struct{} // when non-nil, each Tick waits for a receive
	entered  chan struct{} // receives when a held Tick has started
}

func newFakeTicker() *fakeTicker { return &fakeTicker{ticked: make(chan struct{}, 100)} }

func (f *fakeTicker) Tick(ctx context.Context, store *state.Store) (Result, error) {
	if f.hold != nil {
		f.entered <- struct{}{}
		select {
		case <-f.hold:
		case <-ctx.Done():
		}
	}
	f.mu.Lock()
	f.ticks++
	err := f.err
	f.mu.Unlock()
	f.ticked <- struct{}{}
	return Result{}, err
}

func (f *fakeTicker) OrcaAvailable(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.orcaDown
}

func (f *fakeTicker) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ticks
}

func (f *fakeTicker) waitTick(t *testing.T) {
	t.Helper()
	select {
	case <-f.ticked:
	case <-time.After(5 * time.Second):
		t.Fatal("no Tick happened")
	}
}

func quietLog() *logging.Logger { return logging.New(io.Discard, io.Discard) }

// startLoop runs a Loop until the test ends and returns it with a func that
// stops it and waits for Run to return.
func startLoop(t *testing.T, dir string, interval time.Duration, ticker Ticker, onStatus func(Status)) (*Loop, func()) {
	t.Helper()
	if onStatus == nil {
		onStatus = func(Status) {}
	}
	loop := NewLoop(dir, interval, ticker, quietLog(), onStatus)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		loop.Run(ctx)
		close(done)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return after quit")
			}
		})
	}
	t.Cleanup(stop)
	return loop, stop
}

func TestLoopTicksOnStartAndEveryInterval(t *testing.T) {
	ticker := newFakeTicker()
	startLoop(t, t.TempDir(), 20*time.Millisecond, ticker, nil)
	for range 3 {
		ticker.waitTick(t)
	}
}

func TestQuittingStopsTicksAndReleasesTheLock(t *testing.T) {
	dir := t.TempDir()
	ticker := newFakeTicker()
	_, stop := startLoop(t, dir, 10*time.Millisecond, ticker, nil)
	ticker.waitTick(t)
	stop()

	after := ticker.count()
	time.Sleep(50 * time.Millisecond)
	if got := ticker.count(); got != after {
		t.Errorf("%d Ticks happened after quit", got-after)
	}
	store, err := state.Open(dir)
	if err != nil {
		t.Fatalf("state directory still locked after quit: %v", err)
	}
	store.Close()
}

func TestTickNowTicksWithoutWaitingForTheInterval(t *testing.T) {
	ticker := newFakeTicker()
	loop, _ := startLoop(t, t.TempDir(), time.Hour, ticker, nil)
	ticker.waitTick(t) // the Tick on start
	loop.TickNow()
	ticker.waitTick(t)
}

func TestTheStateDirectoryIsLockedWhileTheLoopIsMidTick(t *testing.T) {
	dir := t.TempDir()
	ticker := newFakeTicker()
	ticker.hold = make(chan struct{})
	ticker.entered = make(chan struct{}, 1)
	startLoop(t, dir, time.Hour, ticker, nil)
	<-ticker.entered

	// This is what `work-agent tick` does first; it must fail fast.
	if _, err := state.Open(dir); !errors.Is(err, state.ErrLocked) {
		t.Fatalf("state.Open mid-Tick: err = %v, want ErrLocked", err)
	}

	ticker.hold <- struct{}{}
	ticker.waitTick(t)
	// Between Ticks the lock is free, so the CLI can Tick.
	store, err := state.Open(dir)
	if err != nil {
		t.Fatalf("state directory locked between Ticks: %v", err)
	}
	store.Close()
}

func TestTheLoopSkipsATickWhileTheCLIHoldsTheLock(t *testing.T) {
	dir := t.TempDir()
	cli, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ticker := newFakeTicker()
	loop, _ := startLoop(t, dir, time.Hour, ticker, nil)
	time.Sleep(50 * time.Millisecond)
	if n := ticker.count(); n != 0 {
		t.Fatalf("Loop Ticked %d times while the CLI held the lock", n)
	}
	cli.Close()
	loop.TickNow()
	ticker.waitTick(t)
}

func seed(t *testing.T, dir string, ids ...string) {
	t.Helper()
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var st workflow.State
	for _, id := range ids {
		st.Items = append(st.Items, workflow.WorkItem{ID: id, Kind: workflow.KindOwnedIssue, State: workflow.PendingWorkspace})
	}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
}

func TestPauseAllPausesEveryWorkItemByTheOperator(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "org/a#1", "org/b#2")
	ticker := newFakeTicker()
	loop, _ := startLoop(t, dir, time.Hour, ticker, nil)
	ticker.waitTick(t)

	if err := loop.PauseAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := state.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range st.Items {
		if w.State != workflow.Paused || w.Pause == nil || !w.Pause.ByOperator {
			t.Errorf("%s: state %s, pause %+v; want Paused by the Operator", w.ID, w.State, w.Pause)
		}
	}
}

func TestPauseAllFailsFastWhileTheCLIHoldsTheLock(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "org/a#1")
	cli, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	loop, _ := startLoop(t, dir, time.Hour, newFakeTicker(), nil)
	if err := loop.PauseAll(context.Background()); !errors.Is(err, state.ErrLocked) {
		t.Errorf("PauseAll err = %v, want ErrLocked", err)
	}
}

func TestDoRunsAnOperatorCommandBetweenTicksUnderTheLock(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "org/a#1")
	ticker := newFakeTicker()
	loop, _ := startLoop(t, dir, time.Hour, ticker, nil)
	ticker.waitTick(t)

	err := loop.Do(context.Background(), func(store *state.Store) error {
		if _, err := state.Open(dir); !errors.Is(err, state.ErrLocked) {
			t.Errorf("the state directory is not locked during Do: %v", err)
		}
		_, err := Pause(store, "org/a#1")
		return err
	})

	if err != nil {
		t.Fatal(err)
	}
	if st, _ := state.Read(dir); st.Items[0].State != workflow.Paused {
		t.Errorf("org/a#1 is %s after Do paused it", st.Items[0].State)
	}
}

func TestDoFailsFastWhileTheCLIHoldsTheLock(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "org/a#1")
	cli, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	loop, _ := startLoop(t, dir, time.Hour, newFakeTicker(), nil)
	ran := false
	err = loop.Do(context.Background(), func(*state.Store) error { ran = true; return nil })
	if !errors.Is(err, state.ErrLocked) || ran {
		t.Errorf("Do err = %v, ran = %v; want ErrLocked without running", err, ran)
	}
}
