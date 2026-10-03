// Package notify defines the Notifier seam the daemon tells the Operator
// things through. The console/JSONL notifier is always on; the tray app adds
// native desktop notifications.
package notify

import (
	"errors"

	"github.com/dwatts1772/work-agent-daemon/internal/logging"
)

// Kind is the condition a Notification is about.
type Kind string

const (
	OrcaUnavailable Kind = "orca-unavailable"
	Paused          Kind = "paused"
	ReadyToMerge    Kind = "ready-to-merge"
	// AgentWaiting is a Workspace whose agent is waiting on the Operator,
	// read from Orca's live agent state.
	AgentWaiting Kind = "agent-waiting"
	Failed       Kind = "failed"
)

// Notification tells the Operator about one condition.
type Notification struct {
	Kind Kind
	// Item is the Work Item the condition is about; empty for daemon-wide
	// conditions such as Orca being unavailable.
	Item  string
	Title string
	Body  string
}

// Key identifies the condition: the same Kind on the same Work Item.
func (n Notification) Key() string { return string(n.Kind) + " " + n.Item }

// Notifier delivers Notifications to the Operator.
type Notifier interface {
	Notify(Notification) error
}

// Once delivers each condition to a Notifier once per occurrence: when it
// first appears among the conditions observed, and again only after it has
// cleared and come back.
type Once struct {
	next   Notifier
	active map[string]bool
}

// NewOnce returns a Once delivering to next.
func NewOnce(next Notifier) *Once {
	return &Once{next: next, active: map[string]bool{}}
}

// Observe takes every condition that holds now and delivers the new ones.
// unknown lists conditions that could not be observed this time, such as an
// agent's state while Orca is unavailable: they stay as they were, neither
// cleared nor delivered. A failed delivery is not retried, so it never turns
// into a duplicate.
func (o *Once) Observe(current, unknown []Notification) error {
	now := map[string]bool{}
	for _, n := range unknown {
		if o.active[n.Key()] {
			now[n.Key()] = true
		}
	}
	var errs []error
	for _, n := range current {
		key := n.Key()
		if !o.active[key] && !now[key] {
			errs = append(errs, o.next.Notify(n))
		}
		now[key] = true
	}
	o.active = now
	return errors.Join(errs...)
}

// Multi delivers every Notification to each of its Notifiers.
type Multi []Notifier

// Notify delivers n to every Notifier, even if one fails.
func (m *Multi) Notify(n Notification) error {
	var errs []error
	for _, x := range *m {
		errs = append(errs, x.Notify(n))
	}
	return errors.Join(errs...)
}

// Log is the console/JSONL Notifier, which is always on: it writes each
// Notification to the daemon's log.
type Log struct {
	log *logging.Logger
}

// NewLog returns a Notifier writing to log.
func NewLog(log *logging.Logger) Log { return Log{log: log} }

// Notify logs n.
func (l Log) Notify(n Notification) error {
	l.log.Info("notify", "kind", n.Kind, "item", n.Item, "title", n.Title, "body", n.Body)
	return nil
}
