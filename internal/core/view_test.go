package core

import (
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

func TestAViewShowsTheItemsWorkspacePRAndHeldWakeReason(t *testing.T) {
	since := time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)
	v := View(workflow.WorkItem{
		ID: "org/a#1", Kind: workflow.KindOwnedIssue, State: workflow.PendingWorkspace,
		Title: "Fix it", IssueURL: "https://github.com/org/a/issues/1",
		Workspace: &workspace.Workspace{Path: "C:/ws/a-1", Branch: "issue-1"},
		PR:        5, PRURL: "https://github.com/org/a/pull/5",
		HeldWake: &workflow.HeldWake{Reason: workflow.WakeIssue, Why: workflow.HoldBackendUnavailable, Since: since},
	})

	want := ItemView{
		ID: "org/a#1", Kind: "OWNED_ISSUE", State: "PENDING_WORKSPACE",
		Title: "Fix it", URL: "https://github.com/org/a/issues/1",
		Workspace: "C:/ws/a-1", Branch: "issue-1",
		PR: 5, PRURL: "https://github.com/org/a/pull/5",
		HeldWake: "issue Wake Held: Orca is unavailable", HeldSince: &since,
		CanPause: true, CanWake: true,
	}
	if v.HeldSince == nil || !v.HeldSince.Equal(since) {
		t.Fatalf("HeldSince = %v, want %v", v.HeldSince, since)
	}
	v.HeldSince = &since
	if v != want {
		t.Errorf("View =\n  %+v\nwant\n  %+v", v, want)
	}
}

func TestAViewOffersOnlyTheCommandsThatApply(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		w                            workflow.WorkItem
		paused                       string
		canPause, canResume, canWake bool
	}{
		{"Woken", workflow.WorkItem{State: workflow.InProgress}, "", true, false, false},
		{"paused by the Operator", workflow.WorkItem{State: workflow.Paused, Pause: &workflow.Pause{ByOperator: true}}, "paused by Operator", false, true, false},
		{"not Eligible", workflow.WorkItem{State: workflow.Paused, Pause: &workflow.Pause{NotEligible: true}}, "not Eligible", true, false, false},
		{"both", workflow.WorkItem{State: workflow.Paused, Pause: &workflow.Pause{ByOperator: true, NotEligible: true}}, "paused by Operator, not Eligible", false, true, false},
		{"DONE", workflow.WorkItem{State: workflow.Done}, "", false, false, false},
	} {
		tc.w.Kind = workflow.KindOwnedIssue
		v := View(tc.w)
		if v.Paused != tc.paused || v.CanPause != tc.canPause || v.CanResume != tc.canResume || v.CanWake != tc.canWake {
			t.Errorf("%s: paused %q pause %v resume %v wake %v; want %q %v %v %v", tc.name,
				v.Paused, v.CanPause, v.CanResume, v.CanWake, tc.paused, tc.canPause, tc.canResume, tc.canWake)
		}
	}
}

func TestOnlyADoneReviewWorkspaceIsSafeToCleanUp(t *testing.T) {
	ws := &workspace.Workspace{Path: "C:/ws/review-9"}
	for _, tc := range []struct {
		name string
		w    workflow.WorkItem
		safe bool
	}{
		{"DONE Review Workspace", workflow.WorkItem{Kind: workflow.KindReviewRequest, State: workflow.Done, Workspace: ws}, true},
		{"Review Request still open", workflow.WorkItem{Kind: workflow.KindReviewRequest, State: workflow.InProgress, Workspace: ws}, false},
		{"DONE Review Request without a Workspace", workflow.WorkItem{Kind: workflow.KindReviewRequest, State: workflow.Done}, false},
		{"DONE Owned Issue", workflow.WorkItem{Kind: workflow.KindOwnedIssue, State: workflow.Done, Workspace: ws}, false},
	} {
		if got := View(tc.w).SafeToCleanUp; got != tc.safe {
			t.Errorf("%s: SafeToCleanUp = %v, want %v", tc.name, got, tc.safe)
		}
	}
}
