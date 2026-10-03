package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// The Operator's per-item commands. The CLI and the tray app's status
// window both run exactly these, so they behave the same. Each takes the
// store whose lock the caller holds.

// ErrNotTracked is returned for a Work Item ID the daemon does not track.
var ErrNotTracked = errors.New("not a tracked Work Item")

// ErrStillNotEligible is returned by Resume when the Operator's pause is
// lifted but the Owned Issue stays Paused because it is not Eligible.
var ErrStillNotEligible = errors.New("still Paused: it is not Eligible (add the Eligibility Label and assign it to the Operator)")

// ErrWakeHeld is returned by Wake when the Wake was Held instead.
var ErrWakeHeld = errors.New("Wake is Held: Orca is unavailable; it is retried on a later Tick")

// Pause Pauses Work Item id by the Operator. It changes only state.json;
// GitHub and the Workspace are never touched.
func Pause(store *state.Store, id string) (workflow.WorkItem, error) {
	return change(store, id, workflow.PauseByOperator)
}

// Resume lifts the Operator's pause of Work Item id. The item is returned
// with ErrStillNotEligible when it stays Paused because it is not Eligible.
func Resume(store *state.Store, id string) (workflow.WorkItem, error) {
	w, err := change(store, id, workflow.ResumeByOperator)
	if err == nil && w.Pause != nil && w.Pause.NotEligible {
		return w, fmt.Errorf("%s is %w", id, ErrStillNotEligible)
	}
	return w, err
}

func change(store *state.Store, id string, f func(workflow.State, string, time.Time) (workflow.State, []workflow.Action, bool)) (workflow.WorkItem, error) {
	current, err := store.Load()
	if err != nil {
		return workflow.WorkItem{}, err
	}
	next, actions, found := f(current, id, time.Now().UTC())
	if !found {
		return workflow.WorkItem{}, fmt.Errorf("%s is %w", id, ErrNotTracked)
	}
	if len(actions) > 0 {
		if err := store.Save(next); err != nil {
			return workflow.WorkItem{}, fmt.Errorf("save state: %w", err)
		}
	}
	w, _ := next.Item(id)
	return w, nil
}

// Wake carries out Work Item id's pending Wake now, exactly as the next
// Tick would — creating its Workspace first if it has none — without
// observing GitHub. Only a Wake the daemon has already decided on can be
// carried out: a Paused, Done or Failed item, or one already Woken, has
// none. The item is returned with ErrWakeHeld when Orca is unavailable.
func (d *Daemon) Wake(ctx context.Context, store *state.Store, id string) (workflow.WorkItem, error) {
	current, err := store.Load()
	if err != nil {
		return workflow.WorkItem{}, err
	}
	w, ok := current.Item(id)
	switch {
	case !ok:
		return workflow.WorkItem{}, fmt.Errorf("%s is %w", id, ErrNotTracked)
	case w.State == workflow.Paused:
		return w, fmt.Errorf("%s is Paused; resume it first", id)
	case w.State == workflow.Done || w.State == workflow.Failed:
		return w, fmt.Errorf("%s is %s; it is never Woken", id, w.State)
	}
	var pending []workflow.Action
	for _, a := range workflow.PendingActions(current) {
		if a.Item.ID == id {
			pending = append(pending, a)
		}
	}
	if len(pending) == 0 {
		return w, fmt.Errorf("%s has nothing to Wake: it is %s", id, w.State)
	}
	held, err := d.act(ctx, store, current, pending)
	if err != nil {
		return w, err
	}
	final, err := store.Load()
	if err != nil {
		return w, err
	}
	w, _ = final.Item(id)
	switch {
	case held:
		return w, fmt.Errorf("%s: %w", id, ErrWakeHeld)
	case w.LastError != "":
		return w, fmt.Errorf("%s: %s", id, w.LastError)
	}
	return w, nil
}
