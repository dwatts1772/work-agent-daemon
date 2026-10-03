package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/core"
	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
)

// eligibleWorld is one Eligible issue, org/a#1, with Orca up or down.
func eligibleWorld(orcaUp bool) testharness.Fixture {
	fx := testharness.Fixture{
		Tokens: map[string]string{operator: token},
		Users:  map[string]string{token: operator},
		Issues: map[string][]testharness.Issue{"org/a": {{
			Number: 1, Title: "Eligible", URL: "https://github.com/org/a/issues/1",
			Labels: []string{"agent-ready"}, Assignees: []string{operator},
		}}},
	}
	if orcaUp {
		fx.Orca = &testharness.Orca{Repos: map[string]string{"repo-a": "org/a"}}
	}
	return fx
}

type windowed struct {
	tray      *tray
	stubs     *testharness.Stubs
	loop      *core.Loop
	window    *statusWindow
	refreshes atomic.Int32
}

// openWindow starts the tray's Tick loop and status window, as serve does,
// with a refresh counter in place of the Wails event.
func openWindow(t *testing.T, fx testharness.Fixture) *windowed {
	t.Helper()
	path, stubs := configWith(t, fx)
	tr, err := prepare(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &windowed{tray: tr, stubs: stubs}
	statuses := make(chan core.Status, 1)
	w.loop, w.window = tr.statusLoop(ctx, statuses, func() { w.refreshes.Add(1) })
	done := make(chan struct{})
	go func() { defer close(done); w.loop.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; tr.close() })
	return w
}

// waitRefresh waits until the window has been told to refresh n times.
func (w *windowed) waitRefresh(t *testing.T, n int32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for w.refreshes.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the window was refreshed %d times, want %d", w.refreshes.Load(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (w *windowed) item(t *testing.T, id string) core.ItemView {
	t.Helper()
	items, err := w.window.Items()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range items {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("%s is not listed in %+v", id, items)
	return core.ItemView{}
}

func TestTheWindowListsEveryWorkItemWithItsHeldWakeAfterEachTick(t *testing.T) {
	w := openWindow(t, eligibleWorld(false))

	w.waitRefresh(t, 1)

	v := w.item(t, "org/a#1")
	if v.State != "PENDING_WORKSPACE" || v.Kind != "OWNED_ISSUE" || v.HeldWake != "issue Wake Held: Orca is unavailable" {
		t.Errorf("org/a#1 = %+v, want a PENDING_WORKSPACE Owned Issue with its Wake Held for Orca", v)
	}

	w.stubs.SetFixture(t, eligibleWorld(true))
	w.loop.TickNow()
	w.waitRefresh(t, 2)

	if v := w.item(t, "org/a#1"); v.State != "IN_PROGRESS" || v.HeldWake != "" || v.Workspace == "" {
		t.Errorf("after the next Tick org/a#1 = %+v, want IN_PROGRESS in its Workspace, nothing Held", v)
	}
}

func TestTheWindowListsNothingBeforeTheFirstTick(t *testing.T) {
	path, _ := configWith(t, eligibleWorld(false))
	tr, err := prepare(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.close()
	_, window := tr.statusLoop(context.Background(), make(chan core.Status, 1), func() {})
	items, err := window.Items()
	if err != nil || items == nil || len(items) != 0 {
		t.Errorf("Items = %#v, %v; want an empty list", items, err)
	}
}

// orcaWakes returns the Wake commands sent to Orca.
func orcaWakes(t *testing.T, stubs *testharness.Stubs) []string {
	t.Helper()
	var cmds []string
	for _, c := range stubs.Calls(t) {
		if c.Bin == "orca" && slices.Contains(c.Args, "terminal") && slices.Contains(c.Args, "create") {
			cmds = append(cmds, c.Args[slices.Index(c.Args, "--command")+1])
		}
	}
	return cmds
}

func TestPauseResumeAndWakeFromTheWindowBehaveLikeTheCLI(t *testing.T) {
	w := openWindow(t, eligibleWorld(false))
	w.waitRefresh(t, 1)

	v, err := w.window.Pause("org/a#1")
	if err != nil || v.State != "PAUSED" || v.Paused != "paused by Operator" || !v.CanResume {
		t.Fatalf("Pause = %+v, %v; want PAUSED by the Operator", v, err)
	}
	if _, err := w.window.Wake("org/a#1"); err == nil || !strings.Contains(err.Error(), "Paused") {
		t.Errorf("Wake of a Paused item: err = %v, want a refusal", err)
	}
	if v, err = w.window.Resume("org/a#1"); err != nil || v.State != "PENDING_WORKSPACE" {
		t.Fatalf("Resume = %+v, %v; want back to PENDING_WORKSPACE", v, err)
	}

	if v, err = w.window.Wake("org/a#1"); !errors.Is(err, core.ErrWakeHeld) || v.HeldWake == "" {
		t.Errorf("Wake with Orca down = %+v, %v; want the Wake Held", v, err)
	}
	w.stubs.SetFixture(t, eligibleWorld(true))
	if v, err = w.window.Wake("org/a#1"); err != nil || v.State != "IN_PROGRESS" {
		t.Fatalf("Wake with Orca up = %+v, %v; want IN_PROGRESS", v, err)
	}
	if got := orcaWakes(t, w.stubs); len(got) != 1 || !strings.Contains(got[0], "/work-item issue org/a#1") {
		t.Errorf("Wakes = %q, want org/a#1's issue Wake once", got)
	}

	for _, cmd := range []func(string) (core.ItemView, error){w.window.Pause, w.window.Resume, w.window.Wake} {
		if _, err := cmd("org/a#99"); !errors.Is(err, core.ErrNotTracked) {
			t.Errorf("command on an untracked item: err = %v, want ErrNotTracked", err)
		}
	}
}

func TestWindowCommandsFailFastWhileTheCLIHoldsTheLock(t *testing.T) {
	w := openWindow(t, eligibleWorld(false))
	w.waitRefresh(t, 1)
	cli, err := state.Open(w.tray.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	if _, err := w.window.Pause("org/a#1"); !errors.Is(err, state.ErrLocked) {
		t.Errorf("Pause while the CLI holds the lock: err = %v, want ErrLocked", err)
	}
}
