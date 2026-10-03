package workspace_test

import (
	"context"
	"io"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/process"
	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

func orcaWith(t *testing.T, fx testharness.Fixture) (*workspace.Orca, *testharness.Stubs) {
	t.Helper()
	stubs := testharness.New(t)
	stubs.SetFixture(t, fx)
	runner := process.NewRunner(stubs.Paths, logging.New(io.Discard, io.Discard))
	return workspace.NewOrca(runner), stubs
}

func TestOrcaIsAvailableWhenItsRuntimeIsReachable(t *testing.T) {
	orca, stubs := orcaWith(t, testharness.Fixture{})
	if !orca.Available(context.Background()) {
		t.Error("Available = false for a reachable Orca runtime")
	}
	calls := stubs.Calls(t)
	if len(calls) != 1 || calls[0].Bin != "orca" || len(calls[0].Args) != 2 || calls[0].Args[0] != "status" || calls[0].Args[1] != "--json" {
		t.Errorf("calls = %+v, want one `orca status --json`", calls)
	}
}

func TestOrcaIsUnavailable(t *testing.T) {
	for name, fx := range map[string]testharness.Fixture{
		"runtime unreachable": {Orca: testharness.OrcaUnreachable},
		"orca fails":          {Orca: testharness.OrcaFailing},
	} {
		orca, _ := orcaWith(t, fx)
		if orca.Available(context.Background()) {
			t.Errorf("%s: Available = true", name)
		}
	}
}

func TestOrcaIsUnavailableWhenNotInstalled(t *testing.T) {
	runner := process.NewRunner(map[string]string{}, logging.New(io.Discard, io.Discard))
	if workspace.NewOrca(runner).Available(context.Background()) {
		t.Error("Available = true with no orca binary")
	}
}
