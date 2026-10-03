package workflow

import (
	"reflect"
	"slices"
	"testing"
)

// ciEvent is the CI observation of PR 9 of org/a#1 at head sha.
func ciEvent(sha string, settled, failed bool) Event {
	return Event{Type: CIObserved, Repo: "org/a", Issue: 1, PR: 9, HeadSHA: sha, Settled: settled, Failed: failed, ObservedAt: t1}
}

// waitingForCI is org/a#1 Woken, with PR 9 linked and head sha recorded.
func waitingForCI(sha string) WorkItem {
	w := linked(inState(ownedIssue("org/a", 1), WaitingForCI), 9)
	w.HeadSHA = sha
	return w
}

func TestASettledFailureDecidesOneCIFailureWakePerHeadSHA(t *testing.T) {
	st := State{Items: []WorkItem{waitingForCI("aaa")}}

	next, actions := Reconcile(st, []Event{ciEvent("aaa", true, true)})

	if len(actions) != 1 || actions[0].Type != CIFailed {
		t.Fatalf("actions = %+v, want one CI_FAILED", actions)
	}
	got, _ := next.Item("org/a#1")
	if got.DueWake != WakeCIFailure || !slices.Contains(got.ProcessedEventIDs, "org/a#9:ci:aaa") {
		t.Errorf("item = %+v, want a ci-failure Wake due and the head's CI marker recorded", got)
	}
	if want := []Action{{Type: Wake, Item: got, Reason: WakeCIFailure}}; !reflect.DeepEqual(PendingActions(next), want) {
		t.Errorf("PendingActions() = %+v, want the ci-failure Wake", PendingActions(next))
	}
	if ref := got.WakeRef(WakeCIFailure); ref != "org/a#9" {
		t.Errorf("WakeRef(ci-failure) = %q, want the PR org/a#9", ref)
	}

	// The Wake is carried out; observing the same failure again does nothing.
	woken := Woken(next, "org/a#1", WakeCIFailure, t2)
	if w, _ := woken.Item("org/a#1"); w.State != AddressingFeedback || w.DueWake != "" {
		t.Errorf("after the Wake item = %s, due %q; want ADDRESSING_FEEDBACK with nothing due", w.State, w.DueWake)
	}
	again, actions := Reconcile(woken, []Event{ciEvent("aaa", true, true)})
	if len(actions) != 0 || !reflect.DeepEqual(again, woken) || len(PendingActions(again)) != 0 {
		t.Errorf("re-observing the same Settled failure acted: %+v", actions)
	}
}

func TestCIThatIsRunningOrGreenNeverWakes(t *testing.T) {
	for _, e := range []Event{ciEvent("aaa", false, true), ciEvent("aaa", false, false), ciEvent("aaa", true, false)} {
		st := State{Items: []WorkItem{waitingForCI("aaa")}}

		next, actions := Reconcile(st, []Event{e})

		if len(actions) != 0 || len(PendingActions(next)) != 0 {
			t.Errorf("settled=%v failed=%v: actions %+v, pending %+v; want none", e.Settled, e.Failed, actions, PendingActions(next))
		}
	}
}

func TestANewHeadSHAMovesTheItemBackToWaitingForCI(t *testing.T) {
	item := waitingForCI("aaa")
	item.State = AddressingFeedback
	item.ProcessedEventIDs = []string{"org/a#9:ci:aaa"}

	next, actions := Reconcile(State{Items: []WorkItem{item}}, []Event{ciEvent("bbb", false, false)})

	got, _ := next.Item("org/a#1")
	if len(actions) != 1 || actions[0].Type != HeadChanged || got.State != WaitingForCI || got.HeadSHA != "bbb" {
		t.Fatalf("actions %+v, item %s at %q; want HEAD_CHANGED to WAITING_FOR_CI at bbb", actions, got.State, got.HeadSHA)
	}

	// The new head failing Wakes again: one Wake per head SHA.
	next, actions = Reconcile(next, []Event{ciEvent("bbb", true, true)})
	if len(actions) != 1 || actions[0].Type != CIFailed {
		t.Errorf("actions = %+v, want CI_FAILED for the new head", actions)
	}
}

func TestANewHeadDropsAWakeDecidedForTheOldOne(t *testing.T) {
	st, _ := Reconcile(State{Items: []WorkItem{waitingForCI("aaa")}}, []Event{ciEvent("aaa", true, true)})
	st = Held(st, "org/a#1", WakeCIFailure, HoldAgentWorking, t1)

	next, _ := Reconcile(st, []Event{ciEvent("bbb", false, false)})

	got, _ := next.Item("org/a#1")
	if got.DueWake != "" || got.HeldWake != nil || len(PendingActions(next)) != 0 {
		t.Errorf("item = %+v; a Wake for a superseded head must be dropped", got)
	}
}

func TestCIIsIgnoredForPausedItemsAndOtherPRs(t *testing.T) {
	paused := waitingForCI("aaa")
	paused.Pause = &Pause{ByOperator: true, ResumeTo: WaitingForCI}
	paused.State = Paused
	other := waitingForCI("aaa")
	other.PR = 8

	for _, item := range []WorkItem{paused, other} {
		st := State{Items: []WorkItem{item}}
		next, actions := Reconcile(st, []Event{ciEvent("bbb", true, true)})
		if len(actions) != 0 || !reflect.DeepEqual(next, st) {
			t.Errorf("item %+v: actions %+v; want none", item, actions)
		}
	}
}

func TestAHeldCIFailureWakeIsRetriedAndOnlyOnce(t *testing.T) {
	st, _ := Reconcile(State{Items: []WorkItem{waitingForCI("aaa")}}, []Event{ciEvent("aaa", true, true)})

	held := Held(st, "org/a#1", WakeCIFailure, HoldAgentWorking, t1)

	w, _ := held.Item("org/a#1")
	if w.HeldWake == nil || w.HeldWake.Why != HoldAgentWorking || w.HeldWake.Reason != WakeCIFailure {
		t.Fatalf("HeldWake = %+v, want a ci-failure Wake Held for agent-working", w.HeldWake)
	}
	if got := PendingActions(held); len(got) != 1 || got[0].Reason != WakeCIFailure {
		t.Errorf("PendingActions() = %+v; a Held Wake is retried on a later Tick", got)
	}
	if _, actions := Reconcile(held, []Event{ciEvent("aaa", true, true)}); len(actions) != 0 {
		t.Errorf("re-observing the failure while Held decided another Wake: %+v", actions)
	}
	if w, _ := Woken(held, "org/a#1", WakeCIFailure, t2).Item("org/a#1"); w.HeldWake != nil {
		t.Errorf("the Wake did not release the hold: %+v", w.HeldWake)
	}
}
