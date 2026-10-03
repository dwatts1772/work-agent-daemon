package workflow

import (
	"reflect"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

func prEvent(typ EventType, repo string, issue, pr int) Event {
	return Event{Type: typ, Repo: repo, Issue: issue, PR: pr, PRURL: "https://github.com/" + repo + "/pull/x", ObservedAt: t1}
}

func inState(w WorkItem, s ItemState) WorkItem {
	w.State = s
	if s != PendingWorkspace {
		w.Workspace = &workspace.Workspace{OrcaIdentityKey: "k", Path: "/ws", Branch: "issue-1", ClaudeSessionID: "s"}
	}
	return w
}

func linked(w WorkItem, pr int) WorkItem {
	w.PR = pr
	w.PRURL = "https://github.com/" + w.Repo + "/pull/x"
	return w
}

func TestADiscoveredPRIsLinkedAndMovesTheItemToWaitingForCI(t *testing.T) {
	item := inState(ownedIssue("org/a", 1), InProgress)

	next, actions := Reconcile(State{Items: []WorkItem{item}}, []Event{prEvent(PRDiscovered, "org/a", 1, 9)})

	want := linked(item, 9)
	want.State = WaitingForCI
	want.UpdatedAt = t1
	if len(actions) != 1 || actions[0].Type != LinkPR || !reflect.DeepEqual(actions[0].Item, want) {
		t.Fatalf("actions = %+v, want one LINK_PR of %+v", actions, want)
	}
	if !reflect.DeepEqual(next.Items, []WorkItem{want}) {
		t.Errorf("items = %+v, want %+v", next.Items, want)
	}

	again, actions := Reconcile(next, []Event{prEvent(PRDiscovered, "org/a", 1, 9)})
	if len(actions) != 0 || !reflect.DeepEqual(again, next) {
		t.Errorf("re-observing the same PR changed something: %+v", actions)
	}
}

func TestPRDiscovery(t *testing.T) {
	cases := []struct {
		name      string
		item      WorkItem
		wantState ItemState
		wantPR    int
		// wantResumeTo is checked when the item is Paused.
		wantResumeTo ItemState
	}{
		{
			name:      "before the first Wake the PR is linked but the Workspace is still created and Woken",
			item:      inState(ownedIssue("org/a", 1), PendingWorkspace),
			wantState: PendingWorkspace, wantPR: 9,
		},
		{
			name:      "a newer open PR replaces the linked one",
			item:      linked(inState(ownedIssue("org/a", 1), WaitingForCI), 8),
			wantState: WaitingForCI, wantPR: 9,
		},
		{
			name: "a Paused item is linked and resumes to WAITING_FOR_CI",
			item: func() WorkItem {
				w := inState(ownedIssue("org/a", 1), Paused)
				w.Pause = &Pause{ByOperator: true, ResumeTo: InProgress, Since: t0}
				return w
			}(),
			wantState: Paused, wantPR: 9, wantResumeTo: WaitingForCI,
		},
		{
			name:      "a linked item already in WAITING_FOR_CI stays there",
			item:      linked(inState(ownedIssue("org/a", 1), WaitingForCI), 9),
			wantState: WaitingForCI, wantPR: 9,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, _ := Reconcile(State{Items: []WorkItem{tc.item}}, []Event{prEvent(PRDiscovered, "org/a", 1, 9)})

			got := next.Items[0]
			if got.State != tc.wantState || got.PR != tc.wantPR {
				t.Errorf("item = %s with PR %d, want %s with PR %d", got.State, got.PR, tc.wantState, tc.wantPR)
			}
			if tc.wantResumeTo != "" && (got.Pause == nil || got.Pause.ResumeTo != tc.wantResumeTo) {
				t.Errorf("pause = %+v, want resuming to %s", got.Pause, tc.wantResumeTo)
			}
		})
	}
}

func TestMergedOrClosedPRAndClosedIssueMoveTheItemToDone(t *testing.T) {
	held := inState(ownedIssue("org/a", 1), PendingWorkspace)
	held.HeldWake = &HeldWake{Reason: WakeIssue, Since: t0, Why: HoldBackendUnavailable}
	paused := linked(inState(ownedIssue("org/a", 1), Paused), 9)
	paused.Pause = &Pause{NotEligible: true, ResumeTo: WaitingForCI, Since: t0}

	cases := []struct {
		name   string
		item   WorkItem
		event  Event
		wantPR int
	}{
		{"merged PR", linked(inState(ownedIssue("org/a", 1), WaitingForCI), 9), prEvent(PRMerged, "org/a", 1, 9), 9},
		{"closed PR", linked(inState(ownedIssue("org/a", 1), WaitingForCI), 9), prEvent(PRClosed, "org/a", 1, 9), 9},
		{"PR merged before it was ever linked", inState(ownedIssue("org/a", 1), InProgress), prEvent(PRMerged, "org/a", 1, 9), 9},
		{"closed issue", inState(ownedIssue("org/a", 1), InProgress), Event{Type: IssueClosed, Repo: "org/a", Issue: 1, ObservedAt: t1}, 0},
		{"closed issue drops a Held Wake", held, Event{Type: IssueClosed, Repo: "org/a", Issue: 1, ObservedAt: t1}, 0},
		{"merged PR ends a pause", paused, prEvent(PRMerged, "org/a", 1, 9), 9},
		{"closed issue on a FAILED item", inState(ownedIssue("org/a", 1), Failed), Event{Type: IssueClosed, Repo: "org/a", Issue: 1, ObservedAt: t1}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, actions := Reconcile(State{Items: []WorkItem{tc.item}}, []Event{tc.event})

			if len(actions) != 1 || actions[0].Type != MarkDone {
				t.Fatalf("actions = %+v, want one DONE", actions)
			}
			got := next.Items[0]
			if got.State != Done || got.Pause != nil || got.HeldWake != nil || got.PR != tc.wantPR {
				t.Errorf("item = %+v, want DONE with PR %d, no pause or Held Wake", got, tc.wantPR)
			}
			if !reflect.DeepEqual(got.Workspace, tc.item.Workspace) {
				t.Errorf("DONE changed the Workspace: %+v", got.Workspace)
			}
			if wake := PendingActions(next); len(wake) != 0 {
				t.Errorf("a DONE item still has Workspace actions: %+v", wake)
			}
		})
	}
}

func TestADoneItemIsFinal(t *testing.T) {
	done := linked(inState(ownedIssue("org/a", 1), Done), 9)
	st := State{Items: []WorkItem{done}}
	for _, e := range []Event{
		prEvent(PRDiscovered, "org/a", 1, 10),
		prEvent(PRMerged, "org/a", 1, 9),
		{Type: IssueClosed, Repo: "org/a", Issue: 1, ObservedAt: t1},
		ineligible("org/a", 1, t1),
		assigned("org/a", 1),
	} {
		if next, actions := Reconcile(st, []Event{e}); len(actions) != 0 || !reflect.DeepEqual(next, st) {
			t.Errorf("%s changed a DONE item: %+v", e.Type, actions)
		}
	}
	if got := Ineligible(st, nil, t1); len(got) != 0 {
		t.Errorf("a DONE item missing from the Eligible issues is reported ineligible: %+v", got)
	}
}

func TestPREventsForUntrackedIssuesDoNothing(t *testing.T) {
	st := State{Items: []WorkItem{inState(ownedIssue("org/a", 1), InProgress)}}
	for _, e := range []Event{prEvent(PRDiscovered, "org/a", 2, 9), prEvent(PRMerged, "org/b", 1, 9)} {
		if _, actions := Reconcile(st, []Event{e}); len(actions) != 0 {
			t.Errorf("%s for %s acted: %+v", e.Type, e.Ref(), actions)
		}
	}
}
