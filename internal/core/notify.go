package core

import (
	"context"

	"github.com/dwatts1772/work-agent-daemon/internal/notify"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

// AddNotifier makes the daemon also deliver Notifications to n, alongside
// the console/JSONL log, which is always on. The tray app adds desktop
// notifications; the headless CLI adds nothing. Call it before the first
// Tick.
func (d *Daemon) AddNotifier(n notify.Notifier) {
	*d.notifiers = append(*d.notifiers, n)
}

// notify delivers the conditions that hold after a Tick, each once per
// occurrence. Orca's availability and agent states are signals read here
// and never stored (ADR-0001). acted reports that the Tick had Workspace
// actions for Orca, and held that it Held them because Orca was
// unavailable.
//
// Orca is consulted only for Work Items that are not Paused, so a Tick with
// only Paused items never touches it; until it is consulted again, Orca is
// taken to be as it last was. If an agent state cannot be read, this Tick
// notifies nothing rather than mistake a waiting agent for one that stopped
// waiting, which would notify it again.
func (d *Daemon) notify(ctx context.Context, st workflow.State, acted, held bool) {
	var watched []workflow.WorkItem
	for _, w := range st.Items {
		if w.Workspace != nil && w.State != workflow.Paused {
			watched = append(watched, w)
		}
	}
	switch {
	case held:
		d.orcaDown = true
	case acted:
		d.orcaDown = false
	case len(watched) > 0:
		available, _ := d.workspaces.Available(ctx)
		d.orcaDown = !available
	}
	agents := map[string]workspace.AgentState{}
	if !d.orcaDown {
		for _, w := range watched {
			s, err := d.workspaces.AgentState(ctx, *w.Workspace)
			if err != nil {
				d.log.Warn("cannot read agent state; skipping notifications this Tick", "item", w.ID, "err", err)
				return
			}
			agents[w.ID] = s
		}
	}
	if err := d.once.Observe(conditions(st, !d.orcaDown, agents)); err != nil {
		d.log.Warn("notification not delivered", "err", err)
	}
}

// conditions are the Notifications for everything that holds: Orca's
// availability, each Work Item's state, and each Workspace's agent state.
func conditions(st workflow.State, orcaAvailable bool, agents map[string]workspace.AgentState) []notify.Notification {
	var out []notify.Notification
	if !orcaAvailable {
		out = append(out, notify.Notification{
			Kind:  notify.OrcaUnavailable,
			Title: "Orca is unavailable",
			Body:  "Wakes are Held until Orca is running again.",
		})
	}
	for _, w := range st.Items {
		switch w.State {
		case workflow.Paused:
			why := "Paused by the Operator."
			if w.Pause != nil && w.Pause.NotEligible {
				why = "Paused: no longer Eligible."
			}
			out = append(out, notify.Notification{Kind: notify.Paused, Item: w.ID, Title: "Paused: " + w.ID, Body: w.Title + "\n" + why})
		case workflow.ReadyToMerge:
			out = append(out, notify.Notification{Kind: notify.ReadyToMerge, Item: w.ID, Title: "Ready to merge: " + w.ID, Body: w.Title})
		case workflow.Failed:
			out = append(out, notify.Notification{Kind: notify.Failed, Item: w.ID, Title: "FAILED: " + w.ID, Body: w.Title + "\n" + w.LastError})
		}
		if agents[w.ID] == workspace.AgentWaiting {
			out = append(out, notify.Notification{Kind: notify.AgentWaiting, Item: w.ID, Title: "Agent waiting: " + w.ID, Body: "Claude is waiting on you in the Workspace for " + w.Title + "."})
		}
	}
	return out
}
