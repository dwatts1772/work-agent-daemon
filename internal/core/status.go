package core

// Status is the overall status the tray icon shows.
type Status string

const (
	StatusOK              Status = "ok"
	StatusHeldWakes       Status = "held-wakes"
	StatusOrcaUnavailable Status = "orca-unavailable"
	StatusNeedsOperator   Status = "needs-operator"
)

// Statuses lists every Status, most urgent first.
var Statuses = []Status{StatusNeedsOperator, StatusOrcaUnavailable, StatusHeldWakes, StatusOK}

// Signals are what the overall Status is derived from. They are read fresh
// on every Tick and never stored (ADR-0001).
type Signals struct {
	OrcaAvailable bool
	// TickFailed means the last Tick could not complete, e.g. because gh
	// can no longer act as the Operator.
	TickFailed bool
	// HeldWakes counts Wakes decided on but deferred.
	HeldWakes int
}

// OverallStatus derives the Status from signals, the most urgent one
// winning.
func OverallStatus(s Signals) Status {
	switch {
	case s.TickFailed:
		return StatusNeedsOperator
	case !s.OrcaAvailable:
		return StatusOrcaUnavailable
	case s.HeldWakes > 0:
		return StatusHeldWakes
	default:
		return StatusOK
	}
}
