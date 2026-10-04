package workflow

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

const quiet = 5 * time.Minute

// observed is an observation of PR 9 of org/a#1 at head sha, its CI Settled
// green, carrying feedback.
func observed(sha string, at time.Time, feedback ...Feedback) Event {
	e := ciEvent(sha, true, false)
	e.ObservedAt, e.QuietPeriod, e.Feedback = at, quiet, feedback
	return e
}

func review(id string, at time.Time) Feedback  { return Feedback{ID: id, Review: true, At: at} }
func comment(id string, at time.Time) Feedback { return Feedback{ID: id, At: at} }

func TestASubmittedReviewDecidesOneFeedbackWake(t *testing.T) {
	st := State{Items: []WorkItem{waitingForCI("aaa")}}

	next, actions := Reconcile(st, []Event{observed("aaa", t1, review("R1", t1))})

	got, _ := next.Item("org/a#1")
	if len(actions) != 1 || actions[0].Type != FeedbackDue || got.DueWake != WakeFeedback {
		t.Fatalf("actions %+v, item due %q; want FEEDBACK with a feedback Wake due", actions, got.DueWake)
	}
	if got.State != WaitingForReview {
		t.Errorf("state = %s, want WAITING_FOR_REVIEW while feedback is outstanding", got.State)
	}
	if want := []Action{{Type: Wake, Item: got, Reason: WakeFeedback}}; !reflect.DeepEqual(PendingActions(next), want) {
		t.Errorf("PendingActions() = %+v, want the feedback Wake", PendingActions(next))
	}
	if ref := got.WakeRef(WakeFeedback); ref != "org/a#9" {
		t.Errorf("WakeRef(feedback) = %q, want the PR org/a#9", ref)
	}

	woken := Woken(next, "org/a#1", WakeFeedback, t2)
	if w, _ := woken.Item("org/a#1"); w.State != AddressingFeedback || w.DueWake != "" {
		t.Errorf("after the Wake item = %s, due %q; want ADDRESSING_FEEDBACK with nothing due", w.State, w.DueWake)
	}

	// Re-observing the same review does nothing.
	again, actions := Reconcile(woken, []Event{observed("aaa", t2, review("R1", t1))})
	if len(actions) != 0 || !reflect.DeepEqual(again, woken) || len(PendingActions(again)) != 0 {
		t.Errorf("re-observing the same review acted: %+v", actions)
	}
}

func TestStandaloneCommentsWakeOnceAfterTheQuietPeriod(t *testing.T) {
	st := State{Items: []WorkItem{waitingForCI("aaa")}}
	c1, c2 := comment("C1", t0), comment("C2", t0.Add(2*time.Minute))

	// Comments still arriving: nothing is decided yet.
	next, actions := Reconcile(st, []Event{observed("aaa", t0.Add(4*time.Minute), c1, c2)})
	if got, _ := next.Item("org/a#1"); len(actions) > 1 || got.DueWake != "" || len(PendingActions(next)) != 0 {
		t.Fatalf("actions %+v, due %q within the Quiet Period; want no Wake", actions, got.DueWake)
	}
	if got, _ := next.Item("org/a#1"); got.State != WaitingForReview {
		t.Errorf("state = %s, want WAITING_FOR_REVIEW while comments are outstanding", got.State)
	}

	// The newest comment has gone quiet: one Wake for the whole batch.
	next, actions = Reconcile(next, []Event{observed("aaa", t0.Add(7*time.Minute), c1, c2)})
	got, _ := next.Item("org/a#1")
	if len(actions) != 1 || actions[0].Type != FeedbackDue || got.DueWake != WakeFeedback {
		t.Fatalf("actions %+v, due %q; want one FEEDBACK once quiet", actions, got.DueWake)
	}
	for _, m := range []string{"org/a#9:comment:C1", "org/a#9:comment:C2"} {
		if !slices.Contains(got.ProcessedEventIDs, m) {
			t.Errorf("markers %q miss %s", got.ProcessedEventIDs, m)
		}
	}

	woken := Woken(next, "org/a#1", WakeFeedback, t2)
	if again, actions := Reconcile(woken, []Event{observed("aaa", t0.Add(30*time.Minute), c1, c2)}); len(actions) != 0 || len(PendingActions(again)) != 0 {
		t.Errorf("re-observing the same comments acted: %+v", actions)
	}
}

func TestAReviewWakesForTheWholeBatchWithoutWaitingForQuiet(t *testing.T) {
	st := State{Items: []WorkItem{waitingForCI("aaa")}}

	next, _ := Reconcile(st, []Event{observed("aaa", t1, comment("C1", t1), review("R1", t1))})

	got, _ := next.Item("org/a#1")
	if got.DueWake != WakeFeedback || !slices.Contains(got.ProcessedEventIDs, "org/a#9:comment:C1") || !slices.Contains(got.ProcessedEventIDs, "org/a#9:review:R1") {
		t.Errorf("item = %+v; a review Wakes at once for every new comment too", got)
	}
}

// A solo Operator's PR has nobody to approve it: green CI and no actionable
// feedback is READY_TO_MERGE without an approval (PRD §4.E, #37).
func TestGreenCIWithNoActionableFeedbackIsReadyToMergeWithoutAnApproval(t *testing.T) {
	green := observed("aaa", t1)

	next, actions := Reconcile(State{Items: []WorkItem{waitingForCI("aaa")}}, []Event{green})

	got, _ := next.Item("org/a#1")
	if len(actions) != 1 || actions[0].Type != CIPassed || got.State != ReadyToMerge {
		t.Fatalf("actions %+v, state %s; want CI_PASSED to READY_TO_MERGE", actions, got.State)
	}
	if len(PendingActions(next)) != 0 {
		t.Errorf("READY_TO_MERGE Woke Claude: %+v", PendingActions(next))
	}
	if again, actions := Reconcile(next, []Event{green}); len(actions) != 0 || !reflect.DeepEqual(again, next) {
		t.Errorf("re-observing changed READY_TO_MERGE: %+v", actions)
	}
}

func TestPRLifecycleStates(t *testing.T) {
	changesRequested := func(e Event) Event { e.ChangesRequested = true; return e }
	running := ciEvent("aaa", false, false)
	running.QuietPeriod = quiet

	for _, tc := range []struct {
		name  string
		from  ItemState
		event Event
		want  ItemState
	}{
		{"green, no feedback", WaitingForCI, observed("aaa", t1), ReadyToMerge},
		{"green, changes requested", WaitingForCI, changesRequested(observed("aaa", t1)), WaitingForReview},
		{"still running", WaitingForCI, running, WaitingForCI},
		{"change request cleared while waiting for review", WaitingForReview, observed("aaa", t1), ReadyToMerge},
		{"changes requested while ready", ReadyToMerge, changesRequested(observed("aaa", t1)), WaitingForReview},
		{"new head while ready", ReadyToMerge, observed("bbb", t1), WaitingForCI},
		{"new head while waiting for review", WaitingForReview, observed("bbb", t1), WaitingForCI},
		{"new head while addressing feedback", AddressingFeedback, observed("bbb", t1), WaitingForCI},
		{"green while addressing feedback", AddressingFeedback, observed("aaa", t1), AddressingFeedback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := waitingForCI("aaa")
			item.State = tc.from
			if tc.event.HeadSHA == "bbb" {
				// Settled on the new head in a later Tick, not this one.
				tc.event.Settled = false
			}

			next, _ := Reconcile(State{Items: []WorkItem{item}}, []Event{tc.event})

			if got, _ := next.Item("org/a#1"); got.State != tc.want {
				t.Errorf("state = %s, want %s", got.State, tc.want)
			}
		})
	}
}

func TestNewFeedbackAfterReadyToMergeWakes(t *testing.T) {
	item := waitingForCI("aaa")
	item.State = ReadyToMerge
	e := observed("aaa", t1, review("R2", t1))

	next, _ := Reconcile(State{Items: []WorkItem{item}}, []Event{e})

	got, _ := next.Item("org/a#1")
	if got.DueWake != WakeFeedback || got.State != WaitingForReview {
		t.Errorf("item = %s, due %q; want WAITING_FOR_REVIEW with a feedback Wake due", got.State, got.DueWake)
	}
}

func TestFeedbackWhileAddressingFeedbackIsDueAndHeldNotLost(t *testing.T) {
	item := waitingForCI("aaa")
	item.State = AddressingFeedback

	next, _ := Reconcile(State{Items: []WorkItem{item}}, []Event{observed("aaa", t1, review("R2", t1))})
	next = Held(next, "org/a#1", WakeFeedback, HoldAgentWorking, t1)

	if got := PendingActions(next); len(got) != 1 || got[0].Reason != WakeFeedback {
		t.Errorf("PendingActions() = %+v; feedback arriving mid-work is Woken once the agent is free", got)
	}
}

func TestANewHeadKeepsAFeedbackWakeDue(t *testing.T) {
	st, _ := Reconcile(State{Items: []WorkItem{waitingForCI("aaa")}}, []Event{observed("aaa", t1, review("R1", t1))})

	next, _ := Reconcile(st, []Event{ciEvent("bbb", false, false)})

	if got, _ := next.Item("org/a#1"); got.State != WaitingForCI || got.DueWake != WakeFeedback {
		t.Errorf("item = %s, due %q; a push does not answer feedback not yet Woken", got.State, got.DueWake)
	}
}

func TestAFailureSeenWhileAFeedbackWakeIsDueWakesAfterIt(t *testing.T) {
	st, _ := Reconcile(State{Items: []WorkItem{waitingForCI("aaa")}}, []Event{observed("aaa", t1, review("R1", t1))})
	failing := observed("aaa", t2, review("R1", t1))
	failing.Failed = true
	st, _ = Reconcile(st, []Event{failing})

	st = Woken(st, "org/a#1", WakeFeedback, t2)
	next, _ := Reconcile(st, []Event{failing})

	if got := PendingActions(next); len(got) != 1 || got[0].Reason != WakeCIFailure {
		t.Fatalf("PendingActions() = %+v; the head's failure still Wakes once", got)
	}
	next = Woken(next, "org/a#1", WakeCIFailure, t2)
	if again, actions := Reconcile(next, []Event{failing}); len(actions) != 0 || len(PendingActions(again)) != 0 {
		t.Errorf("the failure Woke twice: %+v", actions)
	}
}

func TestAFailingReRunTakesTheItemOutOfReadyToMerge(t *testing.T) {
	item := waitingForCI("aaa")
	item.State = ReadyToMerge
	e := observed("aaa", t1)
	e.Failed = true

	next, _ := Reconcile(State{Items: []WorkItem{item}}, []Event{e})

	got, _ := next.Item("org/a#1")
	if got.State != WaitingForCI || got.DueWake != WakeCIFailure {
		t.Errorf("item = %s, due %q; want WAITING_FOR_CI with a ci-failure Wake due", got.State, got.DueWake)
	}
}

func TestASettledFailureWinsOverNewFeedbackInTheSameTick(t *testing.T) {
	e := observed("aaa", t1, review("R1", t1))
	e.Failed = true

	next, _ := Reconcile(State{Items: []WorkItem{waitingForCI("aaa")}}, []Event{e})

	got, _ := next.Item("org/a#1")
	if got.DueWake != WakeCIFailure || slices.Contains(got.ProcessedEventIDs, "org/a#9:review:R1") {
		t.Fatalf("item = %+v; want the ci-failure Wake, the review left for a later Tick", got)
	}
	woken := Woken(next, "org/a#1", WakeCIFailure, t2)
	if again, _ := Reconcile(woken, []Event{e}); func() bool { w, _ := again.Item("org/a#1"); return w.DueWake != WakeFeedback }() {
		t.Error("the review was lost after the ci-failure Wake")
	}
}
