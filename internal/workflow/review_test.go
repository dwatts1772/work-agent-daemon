package workflow

import (
	"reflect"
	"testing"
)

func reviewRequested(sha string, settled bool) Event {
	return Event{
		Type:       ReviewRequested,
		Repo:       "org/a",
		PR:         333,
		PRURL:      "https://github.com/org/a/pull/333",
		Title:      "Someone's change",
		Reviewer:   "dwatts1772",
		HeadSHA:    sha,
		Settled:    settled,
		ObservedAt: t0,
	}
}

func TestAReviewRequestIsTrackedOnlyOnceCIOnItsHeadIsSettled(t *testing.T) {
	if got := Reduce(State{}, reviewRequested("aaa", false)); got != nil {
		t.Fatalf("Reduce() with CI still running = %+v, want nothing", got)
	}

	got := Reduce(State{}, reviewRequested("aaa", true))

	want := []Action{{Type: CreateReviewRequest, Item: WorkItem{
		ID:                "org/a#333",
		Kind:              KindReviewRequest,
		State:             PendingWorkspace,
		Repo:              "org/a",
		Title:             "Someone's change",
		PR:                333,
		PRURL:             "https://github.com/org/a/pull/333",
		HeadSHA:           "aaa",
		ProcessedEventIDs: []string{"org/a#333:review-request:dwatts1772:aaa"},
		CreatedAt:         t0,
		UpdatedAt:         t0,
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Reduce() =\n  %+v\nwant\n  %+v", got, want)
	}
}

func TestAFailedSettledHeadIsReviewedToo(t *testing.T) {
	e := reviewRequested("aaa", true)
	e.Failed = true
	if got := Reduce(State{}, e); len(got) != 1 || got[0].Type != CreateReviewRequest {
		t.Errorf("Reduce() = %+v, want the Review Request tracked", got)
	}
}

func TestReObservingATrackedReviewRequestDoesNothing(t *testing.T) {
	st, _ := Reconcile(State{}, []Event{reviewRequested("aaa", true)})
	reviewing := Woken(WorkspaceCreated(st, "org/a#333", ws, t1), "org/a#333", WakeReview, t2)

	for _, s := range []State{st, reviewing} {
		next, actions := Reconcile(s, []Event{reviewRequested("aaa", true), reviewRequested("aaa", true)})
		if actions != nil || !reflect.DeepEqual(next, s) {
			t.Errorf("re-observing changed %+v: actions %+v", s.Items[0].State, actions)
		}
	}
}

func TestAReviewRequestGetsAReviewWorkspaceAndOneReviewWake(t *testing.T) {
	st, _ := Reconcile(State{}, []Event{reviewRequested("aaa", true)})
	item := st.Items[0]

	want := []Action{
		{Type: CreateWorkspace, Item: item},
		{Type: Wake, Item: item, Reason: WakeReview},
	}
	if got := PendingActions(st); !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingActions() =\n  %+v\nwant\n  %+v", got, want)
	}
	if got := item.WakeRef(WakeReview); got != "org/a#333" {
		t.Errorf("WakeRef(review) = %q, want the PR", got)
	}

	st = Woken(WorkspaceCreated(st, item.ID, ws, t1), item.ID, WakeReview, t2)

	if got, _ := st.Item(item.ID); got.State != Reviewing {
		t.Errorf("state after the review Wake = %s, want REVIEWING", got.State)
	}
	if got := PendingActions(st); got != nil {
		t.Errorf("PendingActions() once REVIEWING = %+v, want none", got)
	}
}

func TestAReviewRequestIsNotTouchedByOwnedIssueObservations(t *testing.T) {
	st, _ := Reconcile(State{}, []Event{reviewRequested("aaa", true)})

	if got := Ineligible(st, nil, t1); got != nil {
		t.Errorf("Ineligible() = %+v; a Review Request is never Eligible or not", got)
	}
	if got := st.Items[0].WatchesCI(); got {
		t.Errorf("WatchesCI() = true; CI on a Review Request never Wakes ci-failure")
	}
}
