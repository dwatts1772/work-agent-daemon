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
	// PendingWorkspace is an Owned Issue not yet Woken in its Workspace;
	// the Workspace may or may not exist yet.
	PendingWorkspace ItemState = "PENDING_WORKSPACE"
	// InProgress is an Owned Issue Woken in its Workspace, with no PR yet.
	InProgress ItemState = "IN_PROGRESS"
	// Failed is a Work Item whose daemon-owned action failed
	// MaxActionFailures times in a row.
	Failed ItemState = "FAILED"
)

// MaxActionFailures is how many consecutive failures of the daemon's own
// action (such as creating a Workspace) move a Work Item to Failed.
const MaxActionFailures = 3

// WakeReason is why a Wake happened.
type WakeReason string

// WakeIssue Wakes Claude to start work on an Owned Issue.
const WakeIssue WakeReason = "issue"

// HoldReason is why a Wake is Held.
type HoldReason string

// HoldBackendUnavailable holds a Wake while Orca is unreachable.
const HoldBackendUnavailable HoldReason = "backend-unavailable"

// HeldWake is a Wake the daemon decided on but deferred; it is retried on a
// later Tick, never dropped.
type HeldWake struct {
	Reason WakeReason `json:"reason"`
	Since  time.Time  `json:"since"`
	Why    HoldReason `json:"why"`
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
	// ProcessedEventIDs are the dedupe markers of every event already
	// applied to this Work Item.
	ProcessedEventIDs []string `json:"processedEventIds"`
	// HeldWake is the Wake waiting for a later Tick, if any.
	HeldWake   *HeldWake  `json:"heldWake,omitempty"`
	LastWakeAt *time.Time `json:"lastWakeAt,omitempty"`
	// LastError is the most recent failure of the daemon's own action.
	LastError                 string    `json:"lastError,omitempty"`
	ConsecutiveActionFailures int       `json:"consecutiveActionFailures"`
	CreatedAt                 time.Time `json:"createdAt"`
	UpdatedAt                 time.Time `json:"updatedAt"`
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

const (
	// CreateOwnedIssue starts tracking Item.
	CreateOwnedIssue ActionType = "CREATE_OWNED_ISSUE"
	// CreateWorkspace creates Item's Workspace.
	CreateWorkspace ActionType = "CREATE_WORKSPACE"
	// Wake Wakes Claude in Item's Workspace for Reason.
	Wake ActionType = "WAKE"
)

// Action is something the reducer decided should happen to Item.
type Action struct {
	Type   ActionType
	Item   WorkItem
	Reason WakeReason
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

// Item returns the Work Item with id.
func (s State) Item(id string) (WorkItem, bool) {
	w, i := s.find(id)
	return w, i >= 0
}

// PendingActions decides the Workspace actions every Owned Issue in
// PendingWorkspace still needs: create its Workspace if none is recorded,
// then Wake Claude in it. They repeat on every Tick until their outcome is
// recorded, which is how a Held Wake is retried.
func PendingActions(state State) []Action {
	var actions []Action
	for _, w := range state.Items {
		if w.Kind != KindOwnedIssue || w.State != PendingWorkspace {
			continue
		}
		if w.Workspace == nil {
			actions = append(actions, Action{Type: CreateWorkspace, Item: w})
		}
		actions = append(actions, Action{Type: Wake, Item: w, Reason: WakeIssue})
	}
	return actions
}

// WakePrompt is the one prompt every Wake sends: the Entry Skill, the Wake
// Reason, and the issue or PR reference.
func WakePrompt(entrySkill string, reason WakeReason, ref string) string {
	return entrySkill + " " + string(reason) + " " + ref
}

// WorkspaceCreated records the Work Item's newly created Workspace.
func WorkspaceCreated(state State, id string, ws workspace.Workspace, now time.Time) State {
	return update(state, id, now, func(w *WorkItem) {
		w.Workspace = &ws
		succeeded(w)
	})
}

// Woken records the Work Item's first Wake, moving it to InProgress.
func Woken(state State, id string, now time.Time) State {
	return update(state, id, now, func(w *WorkItem) {
		w.State = InProgress
		w.LastWakeAt = &now
		succeeded(w)
	})
}

// Held records that the Work Item's Wake is Held. Holding an already Held
// Wake for the same reason changes nothing, so the hold keeps its start.
func Held(state State, id string, reason WakeReason, why HoldReason, now time.Time) State {
	if w, ok := state.Item(id); ok && w.HeldWake != nil && w.HeldWake.Reason == reason && w.HeldWake.Why == why {
		return state
	}
	return update(state, id, now, func(w *WorkItem) {
		w.HeldWake = &HeldWake{Reason: reason, Since: now, Why: why}
	})
}

// ActionFailed records a failure of the daemon's own action on the Work
// Item; the MaxActionFailures-th consecutive failure moves it to Failed.
func ActionFailed(state State, id string, err string, now time.Time) State {
	return update(state, id, now, func(w *WorkItem) {
		w.HeldWake = nil
		w.LastError = err
		w.ConsecutiveActionFailures++
		if w.ConsecutiveActionFailures >= MaxActionFailures {
			w.State = Failed
		}
	})
}

func succeeded(w *WorkItem) {
	w.HeldWake = nil
	w.LastError = ""
	w.ConsecutiveActionFailures = 0
}

// update returns state with change applied to a copy of Work Item id.
func update(state State, id string, now time.Time, change func(*WorkItem)) State {
	next := state.clone()
	if _, i := next.find(id); i >= 0 {
		change(&next.Items[i])
		next.Items[i].UpdatedAt = now
	}
	return next
}
