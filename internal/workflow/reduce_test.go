package workflow

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

var t0 = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

func assigned(repo string, issue int) Event {
	return Event{
		Type:       IssueAssigned,
		Repo:       repo,
		Issue:      issue,
		Title:      "Title " + repo,
		URL:        "https://github.com/" + repo + "/issues/x",
		ObservedAt: t0,
	}
}

func ownedIssue(repo string, issue int, markers ...string) WorkItem {
	return WorkItem{
		ID:                repo + "#" + strconv.Itoa(issue),
		Kind:              KindOwnedIssue,
		State:             PendingWorkspace,
		Repo:              repo,
		Issue:             issue,
		Title:             "Title " + repo,
		IssueURL:          "https://github.com/" + repo + "/issues/x",
		ProcessedEventIDs: markers,
		CreatedAt:         t0,
		UpdatedAt:         t0,
	}
}

func TestReduce(t *testing.T) {
	cases := []struct {
		name  string
		state State
		event Event
		want  []Action
	}{
		{
			name:  "new Eligible issue becomes an Owned Issue in PENDING_WORKSPACE",
			state: State{},
			event: assigned("org/a", 1),
			want:  []Action{{Type: CreateOwnedIssue, Item: ownedIssue("org/a", 1, "org/a#1:assigned")}},
		},
		{
			name:  "already-processed marker does nothing",
			state: State{Items: []WorkItem{ownedIssue("org/a", 1, "org/a#1:assigned")}},
			event: assigned("org/a", 1),
			want:  nil,
		},
		{
			name:  "existing Owned Issue without the marker is not duplicated",
			state: State{Items: []WorkItem{ownedIssue("org/a", 1)}},
			event: assigned("org/a", 1),
			want:  nil,
		},
		{
			name:  "same number in another repo is a different Owned Issue",
			state: State{Items: []WorkItem{ownedIssue("org/a", 1, "org/a#1:assigned")}},
			event: assigned("org/b", 1),
			want:  []Action{{Type: CreateOwnedIssue, Item: ownedIssue("org/b", 1, "org/b#1:assigned")}},
		},
		{
			name:  "another issue in the same repo is a different Owned Issue",
			state: State{Items: []WorkItem{ownedIssue("org/a", 1, "org/a#1:assigned")}},
			event: assigned("org/a", 2),
			want:  []Action{{Type: CreateOwnedIssue, Item: ownedIssue("org/a", 2, "org/a#2:assigned")}},
		},
		{
			name:  "unknown event type does nothing",
			state: State{},
			event: Event{Type: "SOMETHING_ELSE", Repo: "org/a", Issue: 1},
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.state.clone()

			got := Reduce(tc.state, tc.event)

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Reduce() =\n  %+v\nwant\n  %+v", got, tc.want)
			}
			if !reflect.DeepEqual(tc.state, before) {
				t.Errorf("Reduce mutated its input state")
			}
		})
	}
}

func TestEventMarkersAreStable(t *testing.T) {
	cases := []struct {
		event Event
		want  string
	}{
		{assigned("org/a", 1), "org/a#1:assigned"},
		{Event{Type: IssueAssigned, Repo: "org/a", Issue: 1, Title: "renamed", ObservedAt: t0.Add(time.Hour)}, "org/a#1:assigned"},
		{assigned("org/b", 42), "org/b#42:assigned"},
	}
	for _, tc := range cases {
		if got := tc.event.Marker(); got != tc.want {
			t.Errorf("Marker() = %q, want %q", got, tc.want)
		}
	}
}

func TestReconcileAppliesEachEventOnce(t *testing.T) {
	events := []Event{assigned("org/a", 1), assigned("org/a", 1), assigned("org/b", 7)}

	next, actions := Reconcile(State{}, events)

	if len(actions) != 2 {
		t.Fatalf("actions = %+v, want one CreateOwnedIssue per issue", actions)
	}
	want := []WorkItem{ownedIssue("org/a", 1, "org/a#1:assigned"), ownedIssue("org/b", 7, "org/b#7:assigned")}
	if !reflect.DeepEqual(next.Items, want) {
		t.Errorf("items =\n  %+v\nwant\n  %+v", next.Items, want)
	}

	again, actions := Reconcile(next, events)
	if len(actions) != 0 {
		t.Errorf("re-running the same observations produced actions: %+v", actions)
	}
	if !reflect.DeepEqual(again, next) {
		t.Errorf("re-running changed state")
	}
}

func ineligible(repo string, issue int, at time.Time) Event {
	return Event{Type: IssueIneligible, Repo: repo, Issue: issue, ObservedAt: at}
}

func TestLosingEligibilityPausesWithoutTouchingTheWorkspaceAndRegainingItResumes(t *testing.T) {
	item := ownedIssue("org/a", 1, "org/a#1:assigned")
	item.Workspace = &workspace.Workspace{OrcaIdentityKey: "k", Path: "/ws", Branch: "issue-1", ClaudeSessionID: "s"}
	start := State{Items: []WorkItem{item}}
	t1, t2 := t0.Add(time.Hour), t0.Add(2*time.Hour)

	pausedState, actions := Reconcile(start, []Event{ineligible("org/a", 1, t1)})

	if len(actions) != 1 || actions[0].Type != PauseItem {
		t.Fatalf("actions = %+v, want one PAUSE", actions)
	}
	p := pausedState.Items[0]
	if p.State != Paused || p.Pause == nil || !p.Pause.NotEligible {
		t.Fatalf("item = %+v, want PAUSED because not Eligible", p)
	}
	if !reflect.DeepEqual(p.Workspace, item.Workspace) {
		t.Errorf("pausing changed the Workspace: %+v", p.Workspace)
	}

	again, actions := Reconcile(pausedState, []Event{ineligible("org/a", 1, t2)})
	if len(actions) != 0 || !reflect.DeepEqual(again, pausedState) {
		t.Errorf("re-observing lost Eligibility changed something: %+v", actions)
	}

	resumed, actions := Reconcile(pausedState, []Event{assigned("org/a", 1)})

	if len(actions) != 1 || actions[0].Type != ResumeItem {
		t.Fatalf("actions = %+v, want one RESUME", actions)
	}
	r := resumed.Items[0]
	if r.State != PendingWorkspace || r.Pause != nil {
		t.Errorf("item = %+v, want back in PENDING_WORKSPACE", r)
	}
	if !reflect.DeepEqual(r.Workspace, item.Workspace) {
		t.Errorf("resuming changed the Workspace: %+v", r.Workspace)
	}
	if !reflect.DeepEqual(r.ProcessedEventIDs, item.ProcessedEventIDs) {
		t.Errorf("dedupe markers changed: %v", r.ProcessedEventIDs)
	}
}

func TestIneligibleReportsOnlyTrackedOwnedIssuesMissingFromTheObservation(t *testing.T) {
	st := State{Items: []WorkItem{ownedIssue("org/a", 1), ownedIssue("org/a", 2), ownedIssue("org/b", 1)}}

	got := Ineligible(st, []Event{assigned("org/a", 1), assigned("org/c", 9)}, t0)

	var refs []string
	for _, e := range got {
		if e.Type != IssueIneligible {
			t.Errorf("event %+v is not ISSUE_INELIGIBLE", e)
		}
		refs = append(refs, e.Ref())
	}
	if want := []string{"org/a#2", "org/b#1"}; !reflect.DeepEqual(refs, want) {
		t.Errorf("ineligible = %v, want %v", refs, want)
	}
}
