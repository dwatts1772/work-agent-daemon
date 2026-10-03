package core

import "testing"

func TestOverallStatus(t *testing.T) {
	for _, c := range []struct {
		name    string
		signals Signals
		want    Status
	}{
		{"all well", Signals{OrcaAvailable: true}, StatusOK},
		{"Orca unavailable", Signals{}, StatusOrcaUnavailable},
		{"Held Wakes", Signals{OrcaAvailable: true, HeldWakes: 2}, StatusHeldWakes},
		{"Orca unavailable beats Held Wakes", Signals{HeldWakes: 1}, StatusOrcaUnavailable},
		{"a failed Tick needs the Operator", Signals{OrcaAvailable: true, TickFailed: true}, StatusNeedsOperator},
		{"needs Operator beats everything", Signals{TickFailed: true, HeldWakes: 1}, StatusNeedsOperator},
	} {
		if got := OverallStatus(c.signals); got != c.want {
			t.Errorf("%s: OverallStatus(%+v) = %s, want %s", c.name, c.signals, got, c.want)
		}
	}
}
