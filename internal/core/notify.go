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
// only Paused items never touches it. A signal that is not read this Tick —
// Orca's availability when Orca is not consulted, an agent's state while
// Orca is unavailable or when it cannot be read — is unknown: the condition
// stays as it was, so a waiting agent is not notified again once Orca is
// back.
func (d *Daemon) notify(ctx context.Context, st workflow.State, acted, held bool) {
	var watched []workflow.WorkItem
	for _, w := range st.Items {
		if w.Workspace != nil && w.State != workflow.Paused {
			watched = append(watched, w)
		}
	}
	var unknown []notify.Notification
	available := true
	switch {
	case held:
		available = false
	case acted:
	case len(watched) > 0:
		available, _ = d.workspaces.Available(ctx)
	default:
		unknown = append(unknown, orcaUnavailable())
	}
	agents := map[string]workspace.AgentState{}
	for _, w := range watched {
		if !available {
			unknown = append(unknown, agentWaiting(w))
			continue
		}
		s, err := d.workspaces.AgentState(ctx, *w.Workspace)
		if err != nil {
			d.log.Warn("cannot read agent state", "item", w.ID, "err", err)
			unknown = append(unknown, agentWaiting(w))
			continue
		}
		agents[w.ID] = s
	}
	if err := d.once.Observe(conditions(st, available, agents), unknown); err != nil {
		d.log.Warn("notification not delivered", "err", err)
	}
}

// conditions are the Notifications for everything that holds: Orca's
// availability, each Work Item's state, and each Workspace's agent state.
func conditions(st workflow.State, orcaAvailable bool, agents map[string]workspace.AgentState) []notify.Notification {
	var out []notify.Notification
	if !orcaAvailable {
		out = append(out, orcaUnavailable())
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
			out = append(out, agentWaiting(w))
		}
	}
	return out
}

func orcaUnavailable() notify.Notification {
	return notify.Notification{
		Kind:  notify.OrcaUnavailable,
		Title: "Orca is unavailable",
		Body:  "Wakes are Held until Orca is running again.",
	}
}

func agentWaiting(w workflow.WorkItem) notify.Notification {
	return notify.Notification{
		Kind:  notify.AgentWaiting,
		Item:  w.ID,
		Title: "Agent waiting: " + w.ID,
		Body:  "Claude is waiting on you in the Workspace for " + w.Title + ".",
	}
}
