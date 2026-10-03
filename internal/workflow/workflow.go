// Package workflow holds Work Items and the pure reducer that turns
// (state, event) into actions. Nothing here does I/O or reads the clock:
// every input arrives in the State and Event, so the orchestration logic is
// testable without GitHub, Orca, or Claude.
//
// Work Item state is derived only from GitHub observations plus the
// daemon's own records (ADR-0001).
package workflow

import (
	"slices"
	"strconv"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

// Kind distinguishes Owned Issues from Review Requests.
type Kind string

const KindOwnedIssue Kind = "OWNED_ISSUE"

// ItemState is a Work Item's state. It is derived only from GitHub
// observations plus the daemon's own records, such as whether a Workspace
// exists (ADR-0001); it never records what phase Claude is in.
type ItemState string

const (
	// PendingWorkspace is an Owned Issue whose Workspace does not exist yet.
	PendingWorkspace ItemState = "PENDING_WORKSPACE"
	// Paused is a Work Item the daemon will not Wake. Nothing about it,
	// including its Workspace, is changed or deleted while Paused.
	Paused ItemState = "PAUSED"
)

// Pause records why a Work Item is Paused and the state it resumes to. The
// item stays Paused while any reason holds.
type Pause struct {
	// ByOperator is set from `work-agent pause` until `work-agent resume`.
	ByOperator bool `json:"byOperator,omitempty"`
	// NotEligible is set while the Owned Issue is not Eligible on GitHub.
	NotEligible bool `json:"notEligible,omitempty"`
	// ResumeTo is the state the item had when it was Paused.
	ResumeTo ItemState `json:"resumeTo"`
	Since    time.Time `json:"since"`
}

// WorkItem is one tracked unit of work. Only Owned Issues exist so far.
type WorkItem struct {
	// ID is the issue reference, "owner/name#number".
	ID       string    `json:"id"`
	Kind     Kind      `json:"kind"`
	State    ItemState `json:"state"`
	Repo     string    `json:"repo"`
	Issue    int       `json:"issue"`
	Title    string    `json:"title"`
	IssueURL string    `json:"issueUrl"`
	// Workspace is set once the Workspace exists.
	Workspace *workspace.Workspace `json:"workspace,omitempty"`
	// Pause is set while the item is Paused.
	Pause *Pause `json:"pause,omitempty"`
	// ProcessedEventIDs are the dedupe markers of every event already
	// applied to this Work Item.
	ProcessedEventIDs []string  `json:"processedEventIds"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// State is every Work Item the daemon tracks; it is what state.json holds.
type State struct {
	Items []WorkItem `json:"items"`
}

// find returns a copy of item id, safe to modify, and its index.
func (s State) find(id string) (WorkItem, int) {
	for i, w := range s.Items {
		if w.ID == id {
			return w.clone(), i
		}
	}
	return WorkItem{}, -1
}

func (w WorkItem) clone() WorkItem {
	w.ProcessedEventIDs = slices.Clone(w.ProcessedEventIDs)
	if w.Pause != nil {
		p := *w.Pause
		w.Pause = &p
	}
	return w
}

func (s State) clone() State {
	items := make([]WorkItem, len(s.Items))
	for i, w := range s.Items {
		items[i] = w.clone()
	}
	if s.Items == nil {
		items = nil
	}
	return State{Items: items}
}

// EventType names what a Tick observed.
type EventType string

const (
	// IssueAssigned is the observation that an Eligible issue is assigned to
	// the Operator.
	IssueAssigned EventType = "ISSUE_ASSIGNED"
	// IssueIneligible is the observation that a tracked Owned Issue is no
	// longer Eligible: its Eligibility Label was removed, it was reassigned
	// away from the Operator, or it is otherwise missing from the Eligible
	// issues.
	IssueIneligible EventType = "ISSUE_INELIGIBLE"
)

// Event is one observation from a Tick.
type Event struct {
	Type       EventType
	Repo       string
	Issue      int
	Title      string
	URL        string
	ObservedAt time.Time
}

// Ref is the "owner/name#number" the event is about.
func (e Event) Ref() string { return e.Repo + "#" + strconv.Itoa(e.Issue) }

// Marker is the event's stable dedupe marker: observing the same thing again
// on a later Tick yields the same marker.
func (e Event) Marker() string {
	switch e.Type {
	case IssueAssigned:
		return e.Ref() + ":assigned"
	}
	return e.Ref() + ":" + string(e.Type)
}

// ActionType names what the reducer decided should happen.
type ActionType string

const (
	// CreateOwnedIssue starts tracking Item.
	CreateOwnedIssue ActionType = "CREATE_OWNED_ISSUE"
	// PauseItem records Item, now Paused or with a new reason to stay so.
	PauseItem ActionType = "PAUSE"
	// ResumeItem records Item, no longer Paused.
	ResumeItem ActionType = "RESUME"
)

// Action is something the reducer decided should happen to Item; for every
// action but CreateOwnedIssue, Item is the item's new record.
type Action struct {
	Type ActionType
	Item WorkItem
}

// Reduce decides the actions for one event. It never modifies state.
//
// Eligibility is level-triggered: each Tick re-observes it, and an event that
// would not change the item does nothing, so pausing and resuming need no
// dedupe markers of their own.
func Reduce(state State, event Event) []Action {
	switch event.Type {
	case IssueAssigned:
		return reduceAssigned(state, event)
	case IssueIneligible:
		return reduceIneligible(state, event)
	}
	return nil
}

func reduceAssigned(state State, event Event) []Action {
	// The Work Item is keyed by the issue, so an issue already tracked is
	// never recorded twice, whatever markers it carries.
	if w, i := state.find(event.Ref()); i >= 0 {
		if w.Pause == nil || !w.Pause.NotEligible {
			return nil
		}
		w.Pause.NotEligible = false
		return []Action{unpause(w, event.ObservedAt)}
	}
	return []Action{{Type: CreateOwnedIssue, Item: WorkItem{
		ID:                event.Ref(),
		Kind:              KindOwnedIssue,
		State:             PendingWorkspace,
		Repo:              event.Repo,
		Issue:             event.Issue,
		Title:             event.Title,
		IssueURL:          event.URL,
		ProcessedEventIDs: []string{event.Marker()},
		CreatedAt:         event.ObservedAt,
		UpdatedAt:         event.ObservedAt,
	}}}
}

func reduceIneligible(state State, event Event) []Action {
	w, i := state.find(event.Ref())
	if i < 0 || w.Kind != KindOwnedIssue || (w.Pause != nil && w.Pause.NotEligible) {
		return nil
	}
	w = paused(w, event.ObservedAt)
	w.Pause.NotEligible = true
	return []Action{{Type: PauseItem, Item: w}}
}

// paused returns w Paused, keeping any reasons it is already Paused for.
func paused(w WorkItem, at time.Time) WorkItem {
	if w.Pause == nil {
		w.Pause = &Pause{ResumeTo: w.State, Since: at}
		w.State = Paused
	}
	w.UpdatedAt = at
	return w
}

// unpause returns the action recording w, whose pause reasons have just
// changed: resumed if no reason remains, otherwise still Paused.
func unpause(w WorkItem, at time.Time) Action {
	w.UpdatedAt = at
	if w.Pause.ByOperator || w.Pause.NotEligible {
		return Action{Type: PauseItem, Item: w}
	}
	w.State = w.Pause.ResumeTo
	w.Pause = nil
	return Action{Type: ResumeItem, Item: w}
}

// Ineligible returns an IssueIneligible event for every tracked Owned Issue
// missing from assigned, the IssueAssigned events of a complete observation
// of the Eligible issues.
func Ineligible(state State, assigned []Event, at time.Time) []Event {
	seen := map[string]bool{}
	for _, e := range assigned {
		seen[e.Ref()] = true
	}
	var events []Event
	for _, w := range state.Items {
		if w.Kind == KindOwnedIssue && !seen[w.ID] {
			events = append(events, Event{Type: IssueIneligible, Repo: w.Repo, Issue: w.Issue, Title: w.Title, URL: w.IssueURL, ObservedAt: at})
		}
	}
	return events
}

// Apply returns state with the actions' effects on Work Items recorded. It
// never modifies its input.
func Apply(state State, actions []Action) State {
	next := state.clone()
	for _, a := range actions {
		switch a.Type {
		case CreateOwnedIssue:
			next.Items = append(next.Items, a.Item)
		case PauseItem, ResumeItem:
			if _, i := next.find(a.Item.ID); i >= 0 {
				next.Items[i] = a.Item
			}
		}
	}
	return next
}

// PauseByOperator returns state with item id Paused by the Operator, and the
// action taken; none if the Operator had already Paused it. found is false
// if no Work Item has that ID.
func PauseByOperator(state State, id string, at time.Time) (next State, actions []Action, found bool) {
	w, i := state.find(id)
	if i < 0 {
		return state, nil, false
	}
	if w.Pause != nil && w.Pause.ByOperator {
		return state, nil, true
	}
	w = paused(w, at)
	w.Pause.ByOperator = true
	actions = []Action{{Type: PauseItem, Item: w}}
	return Apply(state, actions), actions, true
}

// ResumeByOperator returns state with the Operator's pause of item id lifted,
// and the action taken; none if the Operator had not Paused it. The item
// stays Paused while it is not Eligible. found is false if no Work Item has
// that ID.
func ResumeByOperator(state State, id string, at time.Time) (next State, actions []Action, found bool) {
	w, i := state.find(id)
	if i < 0 {
		return state, nil, false
	}
	if w.Pause == nil || !w.Pause.ByOperator {
		return state, nil, true
	}
	w.Pause.ByOperator = false
	actions = []Action{unpause(w, at)}
	return Apply(state, actions), actions, true
}

// Reconcile reduces and applies a Tick's events in order, returning the new
// state and every action taken.
func Reconcile(state State, events []Event) (State, []Action) {
	var all []Action
	for _, e := range events {
		actions := Reduce(state, e)
		state = Apply(state, actions)
		all = append(all, actions...)
	}
	return state, all
}
