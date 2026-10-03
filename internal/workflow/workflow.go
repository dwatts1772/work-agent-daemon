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

// PendingWorkspace is an Owned Issue whose Workspace does not exist yet.
const PendingWorkspace ItemState = "PENDING_WORKSPACE"

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

func (s State) find(id string) (WorkItem, int) {
	for i, w := range s.Items {
		if w.ID == id {
			return w, i
		}
	}
	return WorkItem{}, -1
}

func (s State) clone() State {
	items := make([]WorkItem, len(s.Items))
	for i, w := range s.Items {
		w.ProcessedEventIDs = slices.Clone(w.ProcessedEventIDs)
		items[i] = w
	}
	if s.Items == nil {
		items = nil
	}
	return State{Items: items}
}

// EventType names what a Tick observed.
type EventType string

// IssueAssigned is the observation that an Eligible issue is assigned to the
// Operator.
const IssueAssigned EventType = "ISSUE_ASSIGNED"

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

// CreateOwnedIssue starts tracking Item.
const CreateOwnedIssue ActionType = "CREATE_OWNED_ISSUE"

// Action is something the reducer decided should happen to Item.
type Action struct {
	Type ActionType
	Item WorkItem
}

// Reduce decides the actions for one event. It never modifies state.
func Reduce(state State, event Event) []Action {
	if event.Type != IssueAssigned {
		return nil
	}
	marker := event.Marker()
	// The Work Item is keyed by the issue, so an issue already tracked is
	// never recorded twice, whatever markers it carries.
	if _, i := state.find(event.Ref()); i >= 0 {
		return nil
	}
	return []Action{{Type: CreateOwnedIssue, Item: WorkItem{
		ID:                event.Ref(),
		Kind:              KindOwnedIssue,
		State:             PendingWorkspace,
		Repo:              event.Repo,
		Issue:             event.Issue,
		Title:             event.Title,
		IssueURL:          event.URL,
		ProcessedEventIDs: []string{marker},
		CreatedAt:         event.ObservedAt,
		UpdatedAt:         event.ObservedAt,
	}}}
}

// Apply returns state with the actions' effects on Work Items recorded. It
// never modifies its input.
func Apply(state State, actions []Action) State {
	next := state.clone()
	for _, a := range actions {
		if a.Type == CreateOwnedIssue {
			next.Items = append(next.Items, a.Item)
		}
	}
	return next
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
