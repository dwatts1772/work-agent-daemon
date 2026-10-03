package workflow

import (
	"reflect"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

var (
	t1 = t0.Add(time.Minute)
	t2 = t0.Add(2 * time.Minute)
	ws = workspace.Workspace{OrcaIdentityKey: "wt2:local:1", Path: "/ws/issue-1", Branch: "issue-1", ClaudeSessionID: "6f1c1f51-7d3e-4b8e-9a43-1d2f3c4b5a69"}
)

func TestPendingActionsCreateAndWakeEachWorkspacelessOwnedIssue(t *testing.T) {
	fresh := ownedIssue("org/a", 1)
	created := ownedIssue("org/a", 2)
	created.Workspace = &ws
	inProgress := ownedIssue("org/a", 3)
	inProgress.State = InProgress
	inProgress.Workspace = &ws
	failed := ownedIssue("org/a", 4)
	failed.State = Failed
	st := State{Items: []WorkItem{fresh, created, inProgress, failed}}

	got := PendingActions(st)

	want := []Action{
		{Type: CreateWorkspace, Item: fresh},
		{Type: Wake, Item: fresh, Reason: WakeIssue},
		{Type: Wake, Item: created, Reason: WakeIssue},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PendingActions() =\n  %+v\nwant\n  %+v", got, want)
	}
}

func TestWakePromptIsTheEntrySkillWithReasonAndRef(t *testing.T) {
	if got := WakePrompt("/work-item", WakeIssue, "org/project-a#428"); got != "/work-item issue org/project-a#428" {
		t.Errorf("WakePrompt() = %q", got)
	}
}

func TestWorkspaceCreatedRecordsTheWorkspaceOnce(t *testing.T) {
	st := State{Items: []WorkItem{ownedIssue("org/a", 1)}}

	next := WorkspaceCreated(st, "org/a#1", ws, t1)

	got := next.Items[0]
	if got.Workspace == nil || *got.Workspace != ws {
		t.Fatalf("Workspace = %+v, want %+v", got.Workspace, ws)
	}
	if got.State != PendingWorkspace || !got.UpdatedAt.Equal(t1) {
		t.Errorf("item = %+v; still PENDING_WORKSPACE until Woken", got)
	}
	if st.Items[0].Workspace != nil {
		t.Error("WorkspaceCreated mutated its input")
	}
	if acts := PendingActions(next); len(acts) != 1 || acts[0].Type != Wake {
		t.Errorf("after creation PendingActions() = %+v, want only the Wake", acts)
	}
}

func TestWokenMovesTheOwnedIssueToInProgress(t *testing.T) {
	item := ownedIssue("org/a", 1)
	item.Workspace = &ws
	item.HeldWake = &HeldWake{Reason: WakeIssue, Since: t0, Why: HoldBackendUnavailable}
	item.ConsecutiveActionFailures = 2
	item.LastError = "boom"
	st := State{Items: []WorkItem{item}}

	next := Woken(st, "org/a#1", WakeIssue, t2)

	got := next.Items[0]
	if got.State != InProgress {
		t.Errorf("State = %s, want IN_PROGRESS", got.State)
	}
	if got.LastWakeAt == nil || !got.LastWakeAt.Equal(t2) {
		t.Errorf("LastWakeAt = %v, want %v", got.LastWakeAt, t2)
	}
	if got.HeldWake != nil || got.ConsecutiveActionFailures != 0 || got.LastError != "" {
		t.Errorf("Woken item keeps a hold or failures: %+v", got)
	}
	if acts := PendingActions(next); len(acts) != 0 {
		t.Errorf("Woken item still has pending actions: %+v", acts)
	}
}

func TestHeldRecordsTheHoldAndKeepsItsStartAcrossTicks(t *testing.T) {
	st := State{Items: []WorkItem{ownedIssue("org/a", 1)}}

	first := Held(st, "org/a#1", WakeIssue, HoldBackendUnavailable, t1)
	second := Held(first, "org/a#1", WakeIssue, HoldBackendUnavailable, t2)

	want := &HeldWake{Reason: WakeIssue, Since: t1, Why: HoldBackendUnavailable}
	if !reflect.DeepEqual(first.Items[0].HeldWake, want) {
		t.Errorf("HeldWake = %+v, want %+v", first.Items[0].HeldWake, want)
	}
	if first.Items[0].State != PendingWorkspace {
		t.Errorf("a Held Wake changed the state to %s", first.Items[0].State)
	}
	if !reflect.DeepEqual(second, first) {
		t.Errorf("holding again changed the item:\n  %+v\nwant\n  %+v", second.Items[0], first.Items[0])
	}
}

func TestAHeldWakeKeepsItsPlaceInLineWhenHeldForAnotherReason(t *testing.T) {
	st := State{Items: []WorkItem{ownedIssue("org/a", 1)}}

	st = Held(st, "org/a#1", WakeIssue, HoldBackendUnavailable, t1)
	st = Held(st, "org/a#1", WakeIssue, HoldCapacity, t2)

	want := &HeldWake{Reason: WakeIssue, Since: t1, Why: HoldCapacity}
	if !reflect.DeepEqual(st.Items[0].HeldWake, want) {
		t.Errorf("HeldWake = %+v, want %+v", st.Items[0].HeldWake, want)
	}
}

func TestRepeatedActionFailuresMoveTheItemToFailed(t *testing.T) {
	st := State{Items: []WorkItem{ownedIssue("org/a", 1)}}

	for i := 1; i < MaxActionFailures; i++ {
		st = ActionFailed(st, "org/a#1", "orca worktree create: boom", t1)
		if got := st.Items[0]; got.State != PendingWorkspace || got.ConsecutiveActionFailures != i {
			t.Fatalf("after %d failures item = %s with %d failures", i, got.State, got.ConsecutiveActionFailures)
		}
	}
	st = ActionFailed(st, "org/a#1", "orca worktree create: boom", t2)

	got := st.Items[0]
	if got.State != Failed || got.LastError != "orca worktree create: boom" || !got.UpdatedAt.Equal(t2) {
		t.Errorf("after %d failures item = %+v, want FAILED with the last error", MaxActionFailures, got)
	}
	if acts := PendingActions(st); len(acts) != 0 {
		t.Errorf("FAILED item still has pending actions: %+v", acts)
	}
}

func TestASuccessResetsTheFailureCount(t *testing.T) {
	st := State{Items: []WorkItem{ownedIssue("org/a", 1)}}
	st = ActionFailed(st, "org/a#1", "boom", t1)

	st = WorkspaceCreated(st, "org/a#1", ws, t2)

	if got := st.Items[0]; got.ConsecutiveActionFailures != 0 || got.LastError != "" {
		t.Errorf("failures not reset after a successful action: %+v", got)
	}
}
