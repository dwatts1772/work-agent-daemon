package workflow

import (
	"reflect"
	"testing"
	"time"
)

// requestedAt is the Operator's review request on PR 333 at head sha,
// observed at at with CI Settled or not.
func requestedAt(sha string, settled bool, at time.Time) Event {
	e := reviewRequested(sha, settled)
	e.QuietPeriod, e.ObservedAt = quiet, at
	return e
}

// reviewing is a Review Request Woken in its Review Workspace for head aaa.
func reviewing() State {
	st, _ := Reconcile(State{}, []Event{requestedAt("aaa", true, t0)})
	return Woken(WorkspaceCreated(st, "org/a#333", ws, t1), "org/a#333", WakeReview, t2)
}

// wakes returns the Wakes PendingActions decides on.
func wakes(st State) []WakeReason {
	var got []WakeReason
	for _, a := range PendingActions(st) {
		if a.Type == Wake {
			got = append(got, a.Reason)
		}
	}
	return got
}

// woken carries out every Wake PendingActions decides on at now.
func woken(st State, now time.Time) State {
	for _, a := range PendingActions(st) {
		if a.Type == Wake {
			st = Woken(st, a.Item.ID, a.Reason, now)
		}
	}
	return st
}

func TestANewHeadWhileRequestedWakesTheReviewWorkspaceOnceAfterTheQuietPeriod(t *testing.T) {
	st := reviewing()
	pushed := t0.Add(10 * time.Minute)

	st, _ = Reconcile(st, []Event{requestedAt("bbb", true, pushed)})
	if got := wakes(st); got != nil {
		t.Fatalf("Wakes %v within the Quiet Period of the push", got)
	}

	st, _ = Reconcile(st, []Event{requestedAt("bbb", true, pushed.Add(quiet))})
	if got := wakes(st); !reflect.DeepEqual(got, []WakeReason{WakeReview}) {
		t.Fatalf("Wakes %v once the push went quiet, want one review Wake", got)
	}
	st = woken(st, pushed.Add(quiet))

	item, _ := st.Item("org/a#333")
	if item.State != Reviewing || item.HeadSHA != "bbb" || item.Workspace == nil || *item.Workspace != ws {
		t.Errorf("item = %s at %q in %+v, want REVIEWING bbb in the same Review Workspace", item.State, item.HeadSHA, item.Workspace)
	}
	for _, at := range []time.Duration{quiet + time.Minute, time.Hour} {
		st, _ = Reconcile(st, []Event{requestedAt("bbb", true, pushed.Add(at))})
		if got := wakes(st); got != nil {
			t.Errorf("Wakes %v re-observing the reviewed head", got)
		}
	}
}

func TestRapidPushesInsideTheQuietPeriodProduceOneReReview(t *testing.T) {
	st := reviewing()
	base := t0.Add(10 * time.Minute)

	for i, sha := range []string{"bbb", "ccc", "ddd"} {
		st, _ = Reconcile(st, []Event{requestedAt(sha, true, base.Add(time.Duration(i)*2*time.Minute))})
		if got := wakes(st); got != nil {
			t.Fatalf("Wakes %v after push %s, inside the Quiet Period of the one before", got, sha)
		}
	}
	// Quiet since ddd, but CI on it has not Settled yet.
	st, _ = Reconcile(st, []Event{requestedAt("ddd", false, base.Add(4*time.Minute+quiet))})
	if got := wakes(st); got != nil {
		t.Fatalf("Wakes %v before CI on the new head Settled", got)
	}
	st, _ = Reconcile(st, []Event{requestedAt("ddd", true, base.Add(5*time.Minute+quiet))})
	if got := wakes(st); !reflect.DeepEqual(got, []WakeReason{WakeReview}) {
		t.Fatalf("Wakes %v once quiet and Settled, want one review Wake", got)
	}

	// The Wake is Held while the agent works, and the author pushes again:
	// the Held re-review is for a head that is gone.
	st = Held(st, "org/a#333", WakeReview, HoldAgentWorking, base.Add(5*time.Minute+quiet))
	again := base.Add(6*time.Minute + quiet)
	st, _ = Reconcile(st, []Event{requestedAt("eee", true, again)})
	if got := wakes(st); got != nil {
		t.Fatalf("Wakes %v for a push still inside its Quiet Period", got)
	}
	st, _ = Reconcile(st, []Event{requestedAt("eee", true, again.Add(quiet))})
	st = woken(st, again.Add(quiet))

	if got := wakes(st); got != nil {
		t.Errorf("Wakes %v after the one re-review", got)
	}
	if item, _ := st.Item("org/a#333"); item.State != Reviewing || item.ReviewedHeadSHA != "eee" || item.HeldWake != nil {
		t.Errorf("item = %s, reviewed %q, held %+v; want REVIEWING eee, nothing Held", item.State, item.ReviewedHeadSHA, item.HeldWake)
	}
}

// removed is the observation that PR 333 no longer requests the Operator's
// review: ended if it was merged or closed, reviewedAt when the Operator last
// submitted a review on it, if ever.
func removed(ended bool, reviewedAt time.Time, at time.Time) Event {
	return Event{Type: ReviewRequestRemoved, Repo: "org/a", PR: 333, Ended: ended, OperatorReviewedAt: reviewedAt, ObservedAt: at}
}

func TestARequestClearedByTheOperatorsOwnReviewLeavesTheItemReviewedUntilReRequested(t *testing.T) {
	st := reviewing()
	submitted := t0.Add(20 * time.Minute)

	st, _ = Reconcile(st, []Event{removed(false, submitted, submitted.Add(time.Minute))})
	if item, _ := st.Item("org/a#333"); item.State != Reviewed {
		t.Fatalf("state = %s after the Operator's review cleared the request, want REVIEWED", item.State)
	}
	again, _ := Reconcile(st, []Event{removed(false, submitted, submitted.Add(2*time.Minute))})
	if !reflect.DeepEqual(again, st) || wakes(st) != nil {
		t.Fatalf("re-observing the cleared request changed the item or Woke it")
	}

	// The author re-requests the Operator at the same head.
	reRequested := submitted.Add(time.Hour)
	st, _ = Reconcile(st, []Event{requestedAt("aaa", true, reRequested)})
	st, _ = Reconcile(st, []Event{requestedAt("aaa", true, reRequested.Add(time.Minute))})
	if got := wakes(st); got != nil {
		t.Fatalf("Wakes %v inside the Quiet Period of the re-request", got)
	}
	st, _ = Reconcile(st, []Event{requestedAt("aaa", true, reRequested.Add(quiet))})
	if got := wakes(st); !reflect.DeepEqual(got, []WakeReason{WakeReview}) {
		t.Fatalf("Wakes %v after the re-request went quiet, want one review Wake", got)
	}
	st = woken(st, reRequested.Add(quiet))
	st, _ = Reconcile(st, []Event{requestedAt("aaa", true, reRequested.Add(2*quiet))})
	if item, _ := st.Item("org/a#333"); item.State != Reviewing || wakes(st) != nil {
		t.Errorf("state = %s, Wakes %v; want REVIEWING, Woken once", item.State, wakes(st))
	}
}

func TestARequestEndedAnyOtherWayIsDoneWithoutAWake(t *testing.T) {
	reviewedByOperator := Reduce(reviewing(), removed(false, t2.Add(time.Minute), t2.Add(2*time.Minute)))[0].Item
	// The author pushed, then the Operator reviewed, before the re-review.
	pushed, _ := Reconcile(reviewing(), []Event{requestedAt("bbb", true, t0.Add(time.Hour))})

	for name, c := range map[string]struct {
		st State
		e  Event
	}{
		"removed by someone else":                  {reviewing(), removed(false, time.Time{}, t0.Add(time.Hour))},
		"removed after an earlier Operator review": {reviewing(), removed(false, t0.Add(-time.Hour), t0.Add(time.Hour))},
		"merged or closed while reviewing":         {reviewing(), removed(true, time.Time{}, t0.Add(time.Hour))},
		"merged or closed after a review":          {State{Items: []WorkItem{reviewedByOperator}}, removed(true, t2.Add(time.Minute), t0.Add(time.Hour))},
		"merged with a re-review pending":          {pushed, removed(true, time.Time{}, t0.Add(2*time.Hour))},
		"merged before the first review Wake":      {func() State { st, _ := Reconcile(State{}, []Event{requestedAt("aaa", true, t0)}); return st }(), removed(true, time.Time{}, t1)},
	} {
		next, _ := Reconcile(c.st, []Event{c.e})
		item, _ := next.Item("org/a#333")
		if item.State != Done || wakes(next) != nil {
			t.Errorf("%s: state = %s, Wakes %v; want DONE, no Wake", name, item.State, wakes(next))
		}
		if !reflect.DeepEqual(item.Workspace, c.st.Items[0].Workspace) {
			t.Errorf("%s: Workspace %+v, want it kept", name, item.Workspace)
		}
		if after, _ := Reconcile(next, []Event{requestedAt("ccc", true, t0.Add(3*time.Hour)), requestedAt("ccc", true, t0.Add(4*time.Hour))}); wakes(after) != nil {
			t.Errorf("%s: a Done Review Request was Woken again", name)
		}
	}
}

func TestAnOperatorReviewOfAnEarlierHeadLeavesTheNewHeadToReReviewOnReRequest(t *testing.T) {
	st := reviewing()
	pushed := t0.Add(10 * time.Minute)
	st, _ = Reconcile(st, []Event{requestedAt("bbb", true, pushed), requestedAt("bbb", true, pushed.Add(quiet))})
	st = Held(st, "org/a#333", WakeReview, HoldAgentWorking, pushed.Add(quiet))

	// The Operator submits the review of aaa, clearing the request.
	e := removed(false, pushed.Add(quiet+time.Minute), pushed.Add(quiet+2*time.Minute))
	e.OperatorReviewedSHA = "aaa"
	st, _ = Reconcile(st, []Event{e})
	if item, _ := st.Item("org/a#333"); item.State != Reviewed || wakes(st) != nil {
		t.Fatalf("state = %s, Wakes %v; want REVIEWED, nothing Woken while unrequested", item.State, wakes(st))
	}
	if again, _ := Reconcile(st, []Event{e}); !reflect.DeepEqual(again, st) {
		t.Fatalf("re-observing the cleared request changed the item")
	}

	reRequested := pushed.Add(time.Hour)
	st, _ = Reconcile(st, []Event{requestedAt("bbb", true, reRequested)})
	if got := wakes(st); got != nil {
		t.Fatalf("Wakes %v inside the Quiet Period of the re-request", got)
	}
	st, _ = Reconcile(st, []Event{requestedAt("bbb", true, reRequested.Add(quiet))})
	if got := wakes(st); !reflect.DeepEqual(got, []WakeReason{WakeReview}) {
		t.Errorf("Wakes %v after the re-request went quiet, want one review Wake for bbb", got)
	}
}

func TestARequestStillOnThePRIsNotEnded(t *testing.T) {
	e := removed(false, time.Time{}, t0.Add(time.Hour))
	e.StillRequested = true // missing from the search, still requested on the PR
	if got := Reduce(reviewing(), e); got != nil {
		t.Errorf("Reduce() = %+v, want nothing while the PR still requests the Operator", got)
	}
}

func TestAnEndedPRIsDoneThoughItStillListsTheRequest(t *testing.T) {
	e := removed(true, time.Time{}, t0.Add(time.Hour))
	e.StillRequested = true
	if got := Reduce(reviewing(), e); len(got) != 1 || got[0].Item.State != Done {
		t.Errorf("Reduce() = %+v, want the Review Request DONE", got)
	}
}
