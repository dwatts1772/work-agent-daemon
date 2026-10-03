package workflow

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

var mvp = FixedSlots{OwnedIssue: 1, ReviewRequest: 1}

func withWorkspace(w WorkItem) WorkItem {
	w.Workspace = &ws
	return w
}

func TestASecondOwnedIssueWakeIsHeldWhileAnOwnedIssueAgentIsWorking(t *testing.T) {
	busy := withWorkspace(ownedIssue("org/a", 1))
	busy.State = InProgress
	next := ownedIssue("org/a", 2)
	pending := PendingActions(State{Items: []WorkItem{busy, next}})
	agents := []AgentSnapshot{{Item: busy.ID, Kind: KindOwnedIssue, State: workspace.AgentWorking}}

	proceed, held := Admit(pending, agents, mvp)

	if len(proceed) != 0 {
		t.Errorf("proceed = %+v, want nothing while the slot is taken", proceed)
	}
	want := []Action{{Type: Wake, Item: next, Reason: WakeIssue}}
	if !reflect.DeepEqual(held, want) {
		t.Errorf("held =\n  %+v\nwant\n  %+v", held, want)
	}
}

func reviewRequest(repo string, pr int) WorkItem {
	return WorkItem{
		ID:        repo + "#" + strconv.Itoa(pr),
		Kind:      KindReviewRequest,
		State:     PendingWorkspace,
		Repo:      repo,
		PR:        pr,
		HeadSHA:   "rrr",
		CreatedAt: t0,
		UpdatedAt: t0,
	}
}

func TestAReviewRequestWakeProceedsWhileAnOwnedIssueAgentIsWorking(t *testing.T) {
	busy := withWorkspace(ownedIssue("org/a", 1))
	busy.State = InProgress
	review := reviewRequest("org/a", 333)
	pending := PendingActions(State{Items: []WorkItem{busy, review}})
	agents := []AgentSnapshot{{Item: busy.ID, Kind: KindOwnedIssue, State: workspace.AgentWorking}}

	proceed, held := Admit(pending, agents, mvp)

	want := []Action{{Type: CreateWorkspace, Item: review}, {Type: Wake, Item: review, Reason: WakeReview}}
	if !reflect.DeepEqual(proceed, want) {
		t.Errorf("proceed =\n  %+v\nwant\n  %+v", proceed, want)
	}
	if len(held) != 0 {
		t.Errorf("held = %+v, want nothing", held)
	}
}

// sharedSlots is a policy whose one pool of slots every kind shares, as a
// resource-based policy would.
type sharedSlots int

func (p sharedSlots) FreeSlots(_ Kind, agents []AgentSnapshot) int {
	free := int(p)
	for _, a := range agents {
		if a.State == workspace.AgentWorking {
			free--
		}
	}
	return free
}

func heldFor(w WorkItem, reason WakeReason, since time.Time) WorkItem {
	w.HeldWake = &HeldWake{Reason: reason, Since: since, Why: HoldCapacity}
	return w
}

func TestHeldWakesReleaseFIFOWithReviewRequestsFirst(t *testing.T) {
	fresh := ownedIssue("org/a", 1)
	newer := heldFor(withWorkspace(ownedIssue("org/a", 2)), WakeIssue, t2)
	older := heldFor(withWorkspace(ownedIssue("org/a", 3)), WakeIssue, t1)
	review := heldFor(reviewRequest("org/a", 333), WakeReview, t2)
	pending := PendingActions(State{Items: []WorkItem{fresh, newer, older, review}})

	proceed, _ := Admit(pending, nil, sharedSlots(1))
	if got := wokenIDs(proceed); !reflect.DeepEqual(got, []string{"org/a#333"}) {
		t.Errorf("one shared slot released %q, want only the Review Request", got)
	}

	proceed, held := Admit(pending, nil, sharedSlots(4))
	var order []string
	for _, a := range proceed {
		order = append(order, string(a.Type)+" "+a.Item.ID)
	}
	want := []string{"CREATE_WORKSPACE org/a#333", "WAKE org/a#333", "WAKE org/a#3", "WAKE org/a#2", "CREATE_WORKSPACE org/a#1", "WAKE org/a#1"}
	if !reflect.DeepEqual(order, want) || len(held) != 0 {
		t.Errorf("released =\n  %q\nwant Review Requests first, then the longest Held\n  %q (held %+v)", order, want, held)
	}

	proceed, held = Admit(pending, nil, mvp)
	if got, want := wokenIDs(proceed), []string{"org/a#333", "org/a#3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fixed slots released %q, want %q", got, want)
	}
	if got, want := wokenIDs(held), []string{"org/a#2", "org/a#1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fixed slots held %q, want %q", got, want)
	}
}

// wokenIDs returns the items of the Wakes among actions, in order.
func wokenIDs(actions []Action) []string {
	var ids []string
	for _, a := range actions {
		if a.Type == Wake {
			ids = append(ids, a.Item.ID)
		}
	}
	return ids
}

func TestAWakeForAWorkingAgentIsLeftToBeHeldAsAgentWorking(t *testing.T) {
	own := withWorkspace(ownedIssue("org/a", 1))
	own.State, own.PR, own.DueWake = WaitingForCI, 60, WakeCIFailure
	pending := PendingActions(State{Items: []WorkItem{own}})
	agents := []AgentSnapshot{{Item: own.ID, Kind: KindOwnedIssue, State: workspace.AgentWorking}}

	proceed, held := Admit(pending, agents, mvp)

	if len(held) != 0 || !reflect.DeepEqual(wokenIDs(proceed), []string{own.ID}) {
		t.Errorf("proceed = %q, held = %q; the Wake must reach the agent-working check", wokenIDs(proceed), wokenIDs(held))
	}
}

func TestAWakeForAWaitingAgentStillNeedsAFreeSlot(t *testing.T) {
	own := withWorkspace(ownedIssue("org/a", 1))
	own.State, own.PR, own.DueWake = WaitingForCI, 60, WakeCIFailure
	other := withWorkspace(ownedIssue("org/a", 2))
	pending := PendingActions(State{Items: []WorkItem{own}})
	agents := []AgentSnapshot{
		{Item: own.ID, Kind: KindOwnedIssue, State: workspace.AgentWaiting},
		{Item: other.ID, Kind: KindOwnedIssue, State: workspace.AgentWorking},
	}

	proceed, held := Admit(pending, agents, mvp)

	if len(proceed) != 0 || !reflect.DeepEqual(wokenIDs(held), []string{own.ID}) {
		t.Errorf("proceed = %q, held = %q; want the Wake Held for capacity", wokenIDs(proceed), wokenIDs(held))
	}
}

func TestIdleAndWaitingAgentsDoNotOccupyASlot(t *testing.T) {
	for _, state := range []workspace.AgentState{workspace.AgentIdle, workspace.AgentWaiting, workspace.AgentNone} {
		other := withWorkspace(ownedIssue("org/a", 1))
		other.State = InProgress
		next := withWorkspace(ownedIssue("org/a", 2))
		pending := PendingActions(State{Items: []WorkItem{other, next}})
		agents := []AgentSnapshot{{Item: other.ID, Kind: KindOwnedIssue, State: state}}

		proceed, held := Admit(pending, agents, mvp)

		want := []Action{{Type: Wake, Item: next, Reason: WakeIssue}}
		if !reflect.DeepEqual(proceed, want) || len(held) != 0 {
			t.Errorf("with the other agent %s: proceed = %+v, held = %+v; want the Wake to proceed", state, proceed, held)
		}
	}
}
