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

const (
	KindOwnedIssue    Kind = "OWNED_ISSUE"
	KindReviewRequest Kind = "REVIEW_REQUEST"
)

// ItemState is a Work Item's state. It is derived only from GitHub
// observations plus the daemon's own records, such as whether a Workspace
// exists (ADR-0001); it never records what phase Claude is in.
type ItemState string

const (
	// PendingWorkspace is a Work Item not yet Woken in its Workspace; the
	// Workspace may or may not exist yet.
	PendingWorkspace ItemState = "PENDING_WORKSPACE"
	// InProgress is an Owned Issue Woken in its Workspace, with no PR yet.
	InProgress ItemState = "IN_PROGRESS"
	// ReadyToMerge is an Owned Issue whose PR has green, Settled CI and no
	// actionable feedback; the Operator merges it.
	ReadyToMerge ItemState = "READY_TO_MERGE"
	// WaitingForCI is an Owned Issue with an open PR linked, whose head's
	// CI has not Settled green.
	WaitingForCI ItemState = "WAITING_FOR_CI"
	// WaitingForReview is an Owned Issue whose PR has green, Settled CI but
	// outstanding feedback or no approval yet.
	WaitingForReview ItemState = "WAITING_FOR_REVIEW"
	// AddressingFeedback is an Owned Issue Woken for feedback or a CI
	// failure on its PR, until the head SHA changes.
	AddressingFeedback ItemState = "ADDRESSING_FEEDBACK"
	// Reviewing is a Review Request Woken in its Review Workspace to review
	// the PR's head.
	Reviewing ItemState = "REVIEWING"
	// Reviewed is a Review Request whose request the Operator's own
	// submitted review cleared. An explicit re-request reviews it again.
	Reviewed ItemState = "REVIEWED"
	// Done is a Work Item whose PR was merged or closed, or whose issue was
	// closed. It is final: the daemon never Wakes it or changes it again.
	Done ItemState = "DONE"
	// Failed is a Work Item whose daemon-owned action failed
	// MaxActionFailures times in a row.
	Failed ItemState = "FAILED"
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

// MaxActionFailures is how many consecutive failures of the daemon's own
// action (such as creating a Workspace) move a Work Item to Failed.
const MaxActionFailures = 3

// WakeReason is why a Wake happened.
type WakeReason string

const (
	// WakeIssue Wakes Claude to start work on an Owned Issue.
	WakeIssue WakeReason = "issue"
	// WakeCIFailure Wakes Claude because CI on its PR's head Settled with a
	// failure.
	WakeCIFailure WakeReason = "ci-failure"
	// WakeFeedback Wakes Claude because its PR received a review, or a batch
	// of comments that has gone quiet.
	WakeFeedback WakeReason = "feedback"
	// WakeReview Wakes Claude to review a Review Request's PR.
	WakeReview WakeReason = "review"
)

// onPR reports whether a Wake for r is about the linked PR rather than the
// issue.
func (r WakeReason) onPR() bool { return r == WakeCIFailure || r == WakeFeedback }

// HoldReason is why a Wake is Held.
type HoldReason string

const (
	// HoldBackendUnavailable holds a Wake while Orca is unreachable.
	HoldBackendUnavailable HoldReason = "backend-unavailable"
	// HoldAgentWorking holds a Wake while Orca reports the Workspace's agent
	// as working.
	HoldAgentWorking HoldReason = "agent-working"
	// HoldCapacity holds a Wake while the CapacityPolicy has no free slot
	// for its kind of Work Item.
	HoldCapacity HoldReason = "capacity"
)

// HeldWake is a Wake the daemon decided on but deferred; it is retried on a
// later Tick, never dropped.
type HeldWake struct {
	Reason WakeReason `json:"reason"`
	Since  time.Time  `json:"since"`
	Why    HoldReason `json:"why"`
}

// WorkItem is one tracked unit of work: an Owned Issue or a Review Request.
type WorkItem struct {
	// ID is the reference of the Owned Issue's issue or the Review
	// Request's PR, "owner/name#number".
	ID       string    `json:"id"`
	Kind     Kind      `json:"kind"`
	State    ItemState `json:"state"`
	Repo     string    `json:"repo"`
	Issue    int       `json:"issue"`
	Title    string    `json:"title"`
	IssueURL string    `json:"issueUrl"`
	// Workspace is set once the Workspace exists.
	Workspace *workspace.Workspace `json:"workspace,omitempty"`
	// PR is the number of the pull request linked to an Owned Issue, if
	// any, or of a Review Request's PR.
	PR    int    `json:"pr,omitempty"`
	PRURL string `json:"prUrl,omitempty"`
	// HeadSHA is the PR's head commit as last observed.
	HeadSHA string `json:"headSha,omitempty"`
	// HeadSeenAt is when the daemon first saw a Review Request's current
	// request at HeadSHA: the item was tracked, its head changed, or the
	// Operator was re-requested. A re-review waits a Quiet Period from it.
	HeadSeenAt time.Time `json:"headSeenAt,omitzero"`
	// ReviewedHeadSHA is the head a Review Request was last reviewed at;
	// cleared when the Operator is re-requested, which that review no
	// longer answers. A re-review is due while it differs from HeadSHA.
	ReviewedHeadSHA string `json:"reviewedHeadSha,omitempty"`
	// Pause is set while the item is Paused.
	Pause *Pause `json:"pause,omitempty"`
	// ProcessedEventIDs are the dedupe markers of every event already
	// applied to this Work Item.
	ProcessedEventIDs []string `json:"processedEventIds"`
	// DueWake is the reason for a Wake the daemon has decided on but not
	// yet carried out, if any; it is retried every Tick until it happens.
	DueWake WakeReason `json:"dueWake,omitempty"`
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

// find returns a copy of item id, safe to modify, and its index.
func (s State) find(id string) (WorkItem, int) {
	for i, w := range s.Items {
		if w.ID == id {
			return w.clone(), i
		}
	}
	return WorkItem{}, -1
}

// Active reports whether w is an Owned Issue not yet Done, the only kind
// of Work Item GitHub observations still change.
func (w WorkItem) Active() bool { return w.Kind == KindOwnedIssue && w.State != Done }

// WatchesPR reports whether its linked PR's head, CI and feedback can still
// change w: it has a PR and is in one of the open-PR states, so neither
// Paused nor Done.
func (w WorkItem) WatchesPR() bool {
	return w.Active() && w.PR != 0 && w.onPR()
}

// onPR reports whether w is in one of the states of an open, linked PR.
func (w WorkItem) onPR() bool {
	switch w.State {
	case WaitingForCI, WaitingForReview, AddressingFeedback, ReadyToMerge:
		return true
	}
	return false
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
	// IssueClosed is the observation that a tracked Owned Issue is closed.
	IssueClosed EventType = "ISSUE_CLOSED"
	// PRDiscovered is the observation of an open PR for a tracked Owned
	// Issue.
	PRDiscovered EventType = "PR_DISCOVERED"
	// PRMerged is the observation that the PR for a tracked Owned Issue is
	// merged.
	PRMerged EventType = "PR_MERGED"
	// PRClosed is the observation that the PR for a tracked Owned Issue was
	// closed without merging.
	PRClosed EventType = "PR_CLOSED"
	// PRObserved is the observation of a tracked Owned Issue's linked PR:
	// its head commit, the CI state of that head, and its feedback.
	PRObserved EventType = "PR_OBSERVED"
	// ReviewRequested is the observation of an open PR in an allowlisted
	// repo on which the Operator is explicitly requested as a reviewer, with
	// the CI state of its head commit.
	ReviewRequested EventType = "REVIEW_REQUESTED"
	// ReviewRequestRemoved is the observation that a tracked Review
	// Request's PR no longer requests the Operator's review: it was merged
	// or closed, the Operator's own review cleared the request, or someone
	// removed it.
	ReviewRequestRemoved EventType = "REVIEW_REQUEST_REMOVED"
)

// Feedback is one review or standalone comment on a PR that counts as
// feedback: from an author allowed to give it, with something to act on.
type Feedback struct {
	// ID is GitHub's ID of the review or comment.
	ID string
	// Review is set for a submitted review, unset for a standalone comment.
	Review bool
	// At is when the review was submitted or the comment created.
	At time.Time
}

// marker is the feedback's dedupe marker on PR pr of repo.
func (f Feedback) marker(repo string, pr int) string {
	kind := "comment"
	if f.Review {
		kind = "review"
	}
	return repo + "#" + strconv.Itoa(pr) + ":" + kind + ":" + f.ID
}

// Event is one observation from a Tick.
type Event struct {
	Type  EventType
	Repo  string
	Issue int
	Title string
	URL   string
	// PR and PRURL identify the pull request of a PR event.
	PR    int
	PRURL string
	// HeadSHA is the PR's head commit; Settled and Failed are its CI state.
	HeadSHA string
	Settled bool
	Failed  bool
	// Feedback is every review and comment on the PR that counts as
	// feedback; Approved is whether the PR is approved with no change
	// requested.
	Feedback []Feedback
	Approved bool
	// QuietPeriod is how long comments must go quiet before the daemon Wakes
	// the Work Item for them.
	QuietPeriod time.Duration
	// Reviewer is the requested reviewer of a ReviewRequested event.
	Reviewer string
	// Ended is set on a ReviewRequestRemoved event when the PR was merged
	// or closed; OperatorReviewedAt is when the Operator last submitted a
	// review on it, zero if never, and OperatorReviewedSHA the head that
	// review was of. StillRequested is set when the PR itself still
	// requests the Operator's review, though the listing missed it.
	Ended               bool
	OperatorReviewedAt  time.Time
	OperatorReviewedSHA string
	StillRequested      bool
	ObservedAt          time.Time
}

// Ref is the "owner/name#number" the event is about: the PR of a
// ReviewRequested or ReviewRequestRemoved event, otherwise the issue.
func (e Event) Ref() string {
	if e.Type == ReviewRequested || e.Type == ReviewRequestRemoved {
		return e.Repo + "#" + strconv.Itoa(e.PR)
	}
	return e.Repo + "#" + strconv.Itoa(e.Issue)
}

// Marker is the event's stable dedupe marker: observing the same thing again
// on a later Tick yields the same marker.
func (e Event) Marker() string {
	switch e.Type {
	case IssueAssigned:
		return e.Ref() + ":assigned"
	case PRObserved:
		return e.Repo + "#" + strconv.Itoa(e.PR) + ":ci:" + e.HeadSHA
	case ReviewRequested:
		return e.Ref() + ":review-request:" + e.Reviewer + ":" + e.HeadSHA
	}
	return e.Ref() + ":" + string(e.Type)
}

// ActionType names what the reducer decided should happen.
type ActionType string

const (
	// CreateOwnedIssue starts tracking Item.
	CreateOwnedIssue ActionType = "CREATE_OWNED_ISSUE"
	// CreateReviewRequest starts tracking Item.
	CreateReviewRequest ActionType = "CREATE_REVIEW_REQUEST"
	// CreateWorkspace creates Item's Workspace.
	CreateWorkspace ActionType = "CREATE_WORKSPACE"
	// Wake Wakes Claude in Item's Workspace for Reason.
	Wake ActionType = "WAKE"
	// PauseItem records Item, now Paused or with a new reason to stay so.
	PauseItem ActionType = "PAUSE"
	// ResumeItem records Item, no longer Paused.
	ResumeItem ActionType = "RESUME"
	// LinkPR records Item with its PR linked.
	LinkPR ActionType = "LINK_PR"
	// MarkDone records Item, now Done.
	MarkDone ActionType = "DONE"
	// HeadChanged records Item with its PR's new head SHA.
	HeadChanged ActionType = "HEAD_CHANGED"
	// CIFailed records Item with a ci-failure Wake due.
	CIFailed ActionType = "CI_FAILED"
	// FeedbackDue records Item with a feedback Wake due.
	FeedbackDue ActionType = "FEEDBACK_DUE"
	// CIPassed records Item, its head's CI Settled green, now waiting for
	// review or ready to merge.
	CIPassed ActionType = "CI_PASSED"
	// ReviewReRequested records Item, its Operator re-requested after their
	// own review.
	ReviewReRequested ActionType = "REVIEW_RE_REQUESTED"
	// ReviewDue records Item with a review Wake due for its new head or
	// re-request.
	ReviewDue ActionType = "REVIEW_DUE"
	// ReviewSubmitted records Item, now Reviewed: the Operator's own
	// review cleared its request.
	ReviewSubmitted ActionType = "REVIEW_SUBMITTED"
)

// Action is something the reducer decided should happen to Item; for every
// action but CreateOwnedIssue and CreateReviewRequest, Item is the item's
// new record.
type Action struct {
	Type   ActionType
	Item   WorkItem
	Reason WakeReason
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
	case PRDiscovered:
		return reducePRDiscovered(state, event)
	case PRMerged, PRClosed, IssueClosed:
		return reduceDone(state, event)
	case PRObserved:
		return reducePR(state, event)
	case ReviewRequested:
		return reduceReviewRequested(state, event)
	case ReviewRequestRemoved:
		return reduceReviewRequestRemoved(state, event)
	}
	return nil
}

// reduceReviewRequested starts tracking a Review Request once CI on its
// head is Settled, pass or fail. The Work Item is keyed by the PR, so a PR
// already tracked is never recorded twice; a tracked one may be due a
// re-review.
func reduceReviewRequested(state State, event Event) []Action {
	if event.HeadSHA == "" {
		return nil
	}
	if w, i := state.find(event.Ref()); i >= 0 {
		return reduceReReview(w, event)
	}
	if !event.Settled {
		return nil
	}
	return []Action{{Type: CreateReviewRequest, Item: WorkItem{
		ID:                event.Ref(),
		Kind:              KindReviewRequest,
		State:             PendingWorkspace,
		Repo:              event.Repo,
		Title:             event.Title,
		PR:                event.PR,
		PRURL:             event.PRURL,
		HeadSHA:           event.HeadSHA,
		HeadSeenAt:        event.ObservedAt,
		ProcessedEventIDs: []string{event.Marker()},
		CreatedAt:         event.ObservedAt,
		UpdatedAt:         event.ObservedAt,
	}}}
}

// reduceReReview decides whether a reviewed Review Request, still
// requested, is due a review of its head again: its head changed, or the
// Operator was re-requested after their own review cleared the request. The
// re-review is due once that request has gone a Quiet Period without a push
// and CI on the head is Settled; a push before it is carried out starts the
// Quiet Period over, so rapid pushes produce one re-review.
func reduceReReview(w WorkItem, event Event) []Action {
	if w.State != Reviewing && w.State != Reviewed {
		return nil
	}
	if w.State == Reviewing && w.ReviewedHeadSHA == "" {
		// Woken before the reviewed head was recorded.
		w.ReviewedHeadSHA = w.HeadSHA
	}
	var last ActionType
	if w.HeadSHA != event.HeadSHA {
		w.HeadSHA, w.HeadSeenAt = event.HeadSHA, event.ObservedAt
		w.DueWake, w.HeldWake = "", nil
		last = HeadChanged
	}
	if w.State == Reviewed && w.ReviewedHeadSHA != "" {
		// Re-requested: the Operator's review no longer answers it.
		w.ReviewedHeadSHA, w.HeadSeenAt = "", event.ObservedAt
		last = ReviewReRequested
	}
	if w.DueWake == "" && w.ReviewedHeadSHA != w.HeadSHA && event.Settled && !event.ObservedAt.Before(w.HeadSeenAt.Add(event.QuietPeriod)) {
		w.DueWake = WakeReview
		last = ReviewDue
	}
	if last == "" {
		return nil
	}
	w.UpdatedAt = event.ObservedAt
	return []Action{{Type: last, Item: w}}
}

// reduceReviewRequestRemoved ends a Review Request whose PR no longer
// requests the Operator's review. A request the Operator's own review
// cleared — one submitted after the current request was first seen — leaves
// a Woken item Reviewed at the head that review was of, so a later
// re-request reviews it again. A request an open PR itself still holds is
// not removed. Any other
// removal, or the PR being merged or closed, moves it to Done from any
// state, dropping any due or Held Wake: it is never Woken again. Its
// Workspace is kept.
func reduceReviewRequestRemoved(state State, event Event) []Action {
	w, i := state.find(event.Ref())
	if i < 0 || w.Kind != KindReviewRequest || w.State == Done || event.StillRequested && !event.Ended {
		return nil
	}
	resting := w.State
	if w.Pause != nil {
		resting = w.Pause.ResumeTo
	}
	w.DueWake, w.HeldWake = "", nil
	w.UpdatedAt = event.ObservedAt
	if !event.Ended && event.OperatorReviewedAt.After(w.HeadSeenAt) && (resting == Reviewing || resting == Reviewed) {
		reviewed := event.OperatorReviewedSHA
		if reviewed == "" {
			reviewed = w.HeadSHA
		}
		if resting == Reviewed && w.ReviewedHeadSHA == reviewed {
			return nil
		}
		w.ReviewedHeadSHA = reviewed
		if w.Pause != nil {
			w.Pause.ResumeTo = Reviewed
		} else {
			w.State = Reviewed
		}
		return []Action{{Type: ReviewSubmitted, Item: w}}
	}
	w.State = Done
	w.Pause = nil
	return []Action{{Type: MarkDone, Item: w}}
}

// reducePR records the linked PR's head SHA and decides what its CI and
// feedback mean, with at most one Wake due at a time:
//
//   - A new head moves the item back to WaitingForCI and drops a ci-failure
//     Wake decided for the old head, which the new commit supersedes; a
//     feedback Wake not yet carried out stays due.
//   - The first time CI on a head Settles with a failure, a ci-failure Wake,
//     and an item not already addressing feedback goes back to WaitingForCI.
//     While another Wake is due the failure waits for a later Tick.
//   - New feedback holding a submitted review, or new comments whose newest
//     is a Quiet Period old, one feedback Wake for the whole batch.
//   - CI Settled green moves the item to ReadyToMerge when the PR is
//     approved with no feedback outstanding, otherwise to WaitingForReview.
func reducePR(state State, event Event) []Action {
	w, i := state.find(event.Ref())
	if i < 0 || !w.WatchesPR() || w.PR != event.PR || event.HeadSHA == "" {
		return nil
	}
	var last ActionType
	if w.HeadSHA != event.HeadSHA {
		w.HeadSHA = event.HeadSHA
		w.State = WaitingForCI
		if w.DueWake == WakeCIFailure {
			w.DueWake, w.HeldWake = "", nil
		}
		last = HeadChanged
	}
	if event.Settled && event.Failed && w.DueWake == "" && !slices.Contains(w.ProcessedEventIDs, event.Marker()) {
		w.ProcessedEventIDs = append(w.ProcessedEventIDs, event.Marker())
		w.DueWake = WakeCIFailure
		if w.State != AddressingFeedback {
			w.State = WaitingForCI
		}
		last = CIFailed
	}
	fresh := w.newFeedback(event.Feedback)
	if len(fresh) > 0 && w.DueWake == "" && batchReady(fresh, event.ObservedAt, event.QuietPeriod) {
		for _, f := range fresh {
			w.ProcessedEventIDs = append(w.ProcessedEventIDs, f.marker(w.Repo, w.PR))
		}
		w.DueWake, fresh = WakeFeedback, nil
		last = FeedbackDue
	}
	if event.Settled && !event.Failed && w.State != AddressingFeedback {
		target := WaitingForReview
		if event.Approved && w.DueWake == "" && len(fresh) == 0 {
			target = ReadyToMerge
		}
		if w.State != target {
			w.State = target
			if last == "" {
				last = CIPassed
			}
		}
	}
	if last == "" {
		return nil
	}
	w.UpdatedAt = event.ObservedAt
	return []Action{{Type: last, Item: w}}
}

// newFeedback returns the feedback not yet acted on.
func (w WorkItem) newFeedback(feedback []Feedback) []Feedback {
	var fresh []Feedback
	for _, f := range feedback {
		if !slices.Contains(w.ProcessedEventIDs, f.marker(w.Repo, w.PR)) {
			fresh = append(fresh, f)
		}
	}
	return fresh
}

// batchReady reports whether the daemon Wakes the Work Item for a batch of
// new feedback now: it holds a submitted review, or its newest comment is a
// Quiet Period old.
func batchReady(batch []Feedback, now time.Time, quiet time.Duration) bool {
	var newest time.Time
	for _, f := range batch {
		if f.Review {
			return true
		}
		if f.At.After(newest) {
			newest = f.At
		}
	}
	return !now.Before(newest.Add(quiet))
}

func reduceAssigned(state State, event Event) []Action {
	// The Work Item is keyed by the issue, so an issue already tracked is
	// never recorded twice, whatever markers it carries.
	if w, i := state.find(event.Ref()); i >= 0 {
		if w.State == Done || w.Pause == nil || !w.Pause.NotEligible {
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
	if i < 0 || !w.Active() || (w.Pause != nil && w.Pause.NotEligible) {
		return nil
	}
	w = paused(w, event.ObservedAt)
	w.Pause.NotEligible = true
	return []Action{{Type: PauseItem, Item: w}}
}

// reducePRDiscovered links an open PR to its Owned Issue. A Woken item moves
// to WaitingForCI; a Paused one will resume there. The move is
// level-triggered, so an item Woken after its PR was linked still moves.
func reducePRDiscovered(state State, event Event) []Action {
	w, i := state.find(event.Ref())
	if i < 0 || !w.Active() {
		return nil
	}
	changed := w.PR != event.PR || w.PRURL != event.PRURL
	w.PR, w.PRURL = event.PR, event.PRURL
	switch {
	case w.State == InProgress:
		w.State, changed = WaitingForCI, true
	case w.Pause != nil && w.Pause.ResumeTo == InProgress:
		w.Pause.ResumeTo, changed = WaitingForCI, true
	}
	if !changed {
		return nil
	}
	w.UpdatedAt = event.ObservedAt
	return []Action{{Type: LinkPR, Item: w}}
}

// reduceDone moves an Owned Issue whose PR was merged or closed, or whose
// issue was closed, to Done from any state, ending any pause and dropping
// any Held Wake: it is never Woken again.
func reduceDone(state State, event Event) []Action {
	w, i := state.find(event.Ref())
	if i < 0 || !w.Active() {
		return nil
	}
	if event.PR != 0 {
		w.PR, w.PRURL = event.PR, event.PRURL
	}
	w.State = Done
	w.Pause = nil
	w.HeldWake = nil
	w.UpdatedAt = event.ObservedAt
	return []Action{{Type: MarkDone, Item: w}}
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
		if w.Active() && !seen[w.ID] {
			events = append(events, Event{Type: IssueIneligible, Repo: w.Repo, Issue: w.Issue, Title: w.Title, URL: w.IssueURL, ObservedAt: at})
		}
	}
	return events
}

// Unrequested returns every tracked Review Request not yet Done missing from
// requested, the ReviewRequested events of a complete observation of the
// review requests.
func Unrequested(state State, requested []Event) []WorkItem {
	seen := map[string]bool{}
	for _, e := range requested {
		seen[e.Ref()] = true
	}
	var items []WorkItem
	for _, w := range state.Items {
		if w.Kind == KindReviewRequest && w.State != Done && !seen[w.ID] {
			items = append(items, w.clone())
		}
	}
	return items
}

// Apply returns state with the actions' effects on Work Items recorded. It
// never modifies its input.
func Apply(state State, actions []Action) State {
	next := state.clone()
	for _, a := range actions {
		switch a.Type {
		case CreateOwnedIssue, CreateReviewRequest:
			next.Items = append(next.Items, a.Item)
		case PauseItem, ResumeItem, LinkPR, MarkDone, HeadChanged, CIFailed, FeedbackDue, CIPassed, ReviewReRequested, ReviewDue, ReviewSubmitted:
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

// Item returns the Work Item with id.
func (s State) Item(id string) (WorkItem, bool) {
	w, i := s.find(id)
	return w, i >= 0
}

// PendingActions decides the Workspace actions Work Items still need: one
// in PendingWorkspace has its Workspace created if none is recorded, then is
// Woken; an Owned Issue with an open PR and a Wake due is Woken. They
// repeat on every Tick until their outcome is recorded, which is how a Held
// Wake is retried.
func PendingActions(state State) []Action {
	var actions []Action
	for _, w := range state.Items {
		reason, ok := WakeDue(w)
		if !ok {
			continue
		}
		if w.State == PendingWorkspace && w.Workspace == nil {
			actions = append(actions, Action{Type: CreateWorkspace, Item: w})
		}
		actions = append(actions, Action{Type: Wake, Item: w, Reason: reason})
	}
	return actions
}

// WakeDue returns the reason w is still to be Woken, if it is.
func WakeDue(w WorkItem) (WakeReason, bool) {
	switch {
	case w.Kind == KindReviewRequest:
		return WakeReview, w.State == PendingWorkspace || (w.State == Reviewing || w.State == Reviewed) && w.DueWake == WakeReview
	case w.Kind != KindOwnedIssue:
		return "", false
	case w.State == PendingWorkspace:
		return WakeIssue, true
	case w.onPR() && w.DueWake != "":
		return w.DueWake, true
	}
	return "", false
}

// WakePrompt is the one prompt every Wake sends: the Entry Skill, the Wake
// Reason, and the issue or PR reference.
func WakePrompt(entrySkill string, reason WakeReason, ref string) string {
	return entrySkill + " " + string(reason) + " " + ref
}

// WakeRef is the reference a Wake for reason names: the issue for an issue
// Wake, the linked PR for a ci-failure or feedback Wake, and the Review
// Request's PR, its ID, for a review Wake.
func (w WorkItem) WakeRef(reason WakeReason) string {
	if reason.onPR() {
		return w.Repo + "#" + strconv.Itoa(w.PR)
	}
	return w.ID
}

// WorkspaceCreated records the Work Item's newly created Workspace.
func WorkspaceCreated(state State, id string, ws workspace.Workspace, now time.Time) State {
	return update(state, id, now, func(w *WorkItem) {
		w.Workspace = &ws
		succeeded(w)
	})
}

// Woken records the Work Item's Wake for reason: the first Wake moves it to
// InProgress, a ci-failure or feedback Wake to AddressingFeedback, a review
// Wake to Reviewing, its head now reviewed.
func Woken(state State, id string, reason WakeReason, now time.Time) State {
	return update(state, id, now, func(w *WorkItem) {
		w.State = InProgress
		switch {
		case reason.onPR():
			w.State = AddressingFeedback
		case reason == WakeReview:
			w.State = Reviewing
			w.ReviewedHeadSHA = w.HeadSHA
		}
		w.DueWake = ""
		w.LastWakeAt = &now
		succeeded(w)
	})
}

// Held records that the Work Item's Wake is Held. Holding an already Held
// Wake for the same reason changes nothing, and holding it for another
// HoldReason keeps its start, so it keeps its place in the FIFO release
// order.
func Held(state State, id string, reason WakeReason, why HoldReason, now time.Time) State {
	since := now
	if w, ok := state.Item(id); ok && w.HeldWake != nil && w.HeldWake.Reason == reason {
		if w.HeldWake.Why == why {
			return state
		}
		since = w.HeldWake.Since
	}
	return update(state, id, now, func(w *WorkItem) {
		w.HeldWake = &HeldWake{Reason: reason, Since: since, Why: why}
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
