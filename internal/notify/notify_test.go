package notify_test

import (
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/notify"
)

// recorder records every Notification it is handed.
type recorder struct{ got []notify.Notification }

func (r *recorder) Notify(n notify.Notification) error {
	r.got = append(r.got, n)
	return nil
}

var (
	orcaDown = notify.Notification{Kind: notify.OrcaUnavailable, Title: "Orca is unavailable"}
	paused   = notify.Notification{Kind: notify.Paused, Item: "org/a#1", Title: "Paused"}
)

func TestAConditionIsDeliveredOnceWhileItHolds(t *testing.T) {
	r := &recorder{}
	once := notify.NewOnce(r)

	once.Observe([]notify.Notification{orcaDown, paused}, nil)
	once.Observe([]notify.Notification{orcaDown, paused}, nil)
	once.Observe([]notify.Notification{paused, orcaDown}, nil)

	if len(r.got) != 2 || r.got[0].Kind != notify.OrcaUnavailable || r.got[1].Kind != notify.Paused {
		t.Fatalf("delivered %v, want Orca unavailable then Paused, once each", r.got)
	}
}

func TestAConditionThatClearsAndReturnsIsANewOccurrence(t *testing.T) {
	r := &recorder{}
	once := notify.NewOnce(r)

	once.Observe([]notify.Notification{orcaDown}, nil)
	once.Observe(nil, nil) // Orca is back
	once.Observe([]notify.Notification{orcaDown}, nil)

	if len(r.got) != 2 {
		t.Fatalf("delivered %d notifications, want 2 (one per occurrence)", len(r.got))
	}
}

func TestTheSameKindOnDifferentWorkItemsIsDeliveredForEach(t *testing.T) {
	r := &recorder{}
	once := notify.NewOnce(r)
	other := paused
	other.Item = "org/a#2"

	once.Observe([]notify.Notification{paused, other}, nil)

	if len(r.got) != 2 {
		t.Fatalf("delivered %d notifications, want one per Work Item", len(r.got))
	}
}

func TestAConditionThatCannotBeObservedIsNeitherClearedNorRedelivered(t *testing.T) {
	r := &recorder{}
	once := notify.NewOnce(r)

	once.Observe([]notify.Notification{orcaDown}, nil)
	once.Observe(nil, []notify.Notification{orcaDown}) // Orca not consulted
	once.Observe([]notify.Notification{orcaDown}, nil)

	if len(r.got) != 1 {
		t.Fatalf("delivered %d notifications, want 1: an unobserved condition still holds", len(r.got))
	}
}
