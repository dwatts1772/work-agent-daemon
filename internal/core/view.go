package core

import (
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// ItemView is one Work Item as the Operator sees it in the status window.
type ItemView struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Title string `json:"title"`
	URL   string `json:"url"`
	// Workspace is the Workspace's path, empty until it exists.
	Workspace string `json:"workspace"`
	Branch    string `json:"branch"`
	PR        int    `json:"pr"`
	PRURL     string `json:"prUrl"`
	// Paused says why the item is Paused, empty when it is not.
	Paused string `json:"paused"`
	// HeldWake says which Wake is Held and why, empty when none is.
	HeldWake  string     `json:"heldWake"`
	HeldSince *time.Time `json:"heldSince"`
	LastError string     `json:"lastError"`
	// SafeToCleanUp marks a DONE Review Workspace: the daemon will never
	// use it again, and it is retained only until the Operator removes it.
	SafeToCleanUp bool `json:"safeToCleanUp"`
	// CanPause, CanResume and CanWake say which Operator commands apply.
	CanPause  bool `json:"canPause"`
	CanResume bool `json:"canResume"`
	CanWake   bool `json:"canWake"`
}

// holdReasons explains each HoldReason to the Operator.
var holdReasons = map[workflow.HoldReason]string{
	workflow.HoldBackendUnavailable: "Orca is unavailable",
}

// View returns w as the Operator sees it.
func View(w workflow.WorkItem) ItemView {
	v := ItemView{
		ID:        w.ID,
		Kind:      string(w.Kind),
		State:     string(w.State),
		Title:     w.Title,
		URL:       w.IssueURL,
		PR:        w.PR,
		PRURL:     w.PRURL,
		Paused:    PausedBecause(w),
		LastError: w.LastError,
		CanPause:  w.State != workflow.Done && (w.Pause == nil || !w.Pause.ByOperator),
		CanResume: w.Pause != nil && w.Pause.ByOperator,
		CanWake:   len(workflow.PendingActions(workflow.State{Items: []workflow.WorkItem{w}})) > 0,
	}
	if w.Workspace != nil {
		v.Workspace, v.Branch = w.Workspace.Path, w.Workspace.Branch
	}
	if h := w.HeldWake; h != nil {
		why, ok := holdReasons[h.Why]
		if !ok {
			why = string(h.Why)
		}
		since := h.Since
		v.HeldWake, v.HeldSince = string(h.Reason)+" Wake Held: "+why, &since
	}
	v.SafeToCleanUp = w.Kind == workflow.KindReviewRequest && w.State == workflow.Done && w.Workspace != nil
	return v
}

// PausedBecause says why w is Paused, or "" when it is not.
func PausedBecause(w workflow.WorkItem) string {
	switch {
	case w.Pause == nil:
		return ""
	case w.Pause.ByOperator && w.Pause.NotEligible:
		return "paused by Operator, not Eligible"
	case w.Pause.ByOperator:
		return "paused by Operator"
	default:
		return "not Eligible"
	}
}
