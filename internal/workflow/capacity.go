package workflow

import (
	"slices"

	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

// AgentSnapshot is a Work Item's agent as Orca reports it this Tick: a
// signal, never stored (ADR-0001).
type AgentSnapshot struct {
	Item  string
	Kind  Kind
	State workspace.AgentState
}

// CapacityPolicy decides how many more Wakes of kind may proceed, given the
// agents Orca reports. It is the seam a smarter policy (RAM, CPU, Claude plan
// usage) plugs into without touching the reducer.
type CapacityPolicy interface {
	FreeSlots(kind Kind, agents []AgentSnapshot) int
}

// FixedSlots is the MVP CapacityPolicy: a fixed number of slots per kind, a
// slot being occupied only by an agent Orca reports as working. Idle agents
// and agents waiting on the Operator free their slot.
type FixedSlots struct {
	OwnedIssue    int
	ReviewRequest int
}

// FreeSlots is kind's slots less its working agents.
func (p FixedSlots) FreeSlots(kind Kind, agents []AgentSnapshot) int {
	free := p.OwnedIssue
	if kind == KindReviewRequest {
		free = p.ReviewRequest
	}
	for _, a := range agents {
		if a.Kind == kind && a.State == workspace.AgentWorking {
			free--
		}
	}
	return free
}

// Admit splits pending, the actions of PendingActions, into those that may
// proceed this Tick and the Wakes to Hold for capacity. Wakes are released
// FIFO, Review Requests first: a Held Wake in order of when it was first
// Held, ahead of Wakes not yet Held. Each Wake let through occupies a slot
// from then on; a Workspace is created only for a Wake that proceeds. A Wake
// whose own agent is working or waiting on the Operator proceeds without a
// slot, to be Held because that agent cannot take it.
func Admit(pending []Action, agents []AgentSnapshot, policy CapacityPolicy) (proceed, held []Action) {
	agents = slices.Clone(agents)
	creates := map[string]Action{}
	var wakes []Action
	for _, a := range pending {
		switch a.Type {
		case CreateWorkspace:
			creates[a.Item.ID] = a
		case Wake:
			wakes = append(wakes, a)
		}
	}
	busy := map[string]bool{}
	for _, s := range agents {
		busy[s.Item] = s.State == workspace.AgentWorking || s.State == workspace.AgentWaiting
	}
	slices.SortStableFunc(wakes, releaseOrder)
	for _, a := range wakes {
		if busy[a.Item.ID] {
			// Its own agent cannot take the Wake, which is Held for that.
			proceed = append(proceed, a)
			continue
		}
		if policy.FreeSlots(a.Item.Kind, agents) <= 0 {
			held = append(held, a)
			continue
		}
		if c, ok := creates[a.Item.ID]; ok {
			proceed = append(proceed, c)
		}
		proceed = append(proceed, a)
		agents = append(agents, AgentSnapshot{Item: a.Item.ID, Kind: a.Item.Kind, State: workspace.AgentWorking})
	}
	return proceed, held
}

// releaseOrder orders Wakes Review Requests first, then Held Wakes oldest
// first, then Wakes not yet Held.
func releaseOrder(a, b Action) int {
	if ra, rb := a.Item.Kind == KindReviewRequest, b.Item.Kind == KindReviewRequest; ra != rb {
		if ra {
			return -1
		}
		return 1
	}
	ha, hb := a.Item.HeldWake, b.Item.HeldWake
	switch {
	case ha != nil && hb != nil:
		return ha.Since.Compare(hb.Since)
	case ha != nil:
		return -1
	case hb != nil:
		return 1
	}
	return 0
}
