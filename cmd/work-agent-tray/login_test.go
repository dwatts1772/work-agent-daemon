package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/core"
	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
)

// tickOnce starts tr's Tick loop, as serve does, and returns the overall
// status once the first Tick has ended.
func tickOnce(t *testing.T, tr *tray) core.Status {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ticked := make(chan struct{}, 1)
	statuses := make(chan core.Status, 1)
	loop, _ := tr.statusLoop(ctx, statuses, func() {
		select {
		case ticked <- struct{}{}:
		default:
		}
	})
	done := make(chan struct{})
	go func() { defer close(done); loop.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-ticked:
		return <-statuses
	case <-time.After(10 * time.Second):
		t.Fatal("no Tick ended within 10s")
		return ""
	}
}

// A login item gets a minimal PATH and no shell profile: with no binaries
// configured, the tray app must still find gh, git, orca and claude by
// absolute path and Tick.
func TestAfterALoginTheTrayAppTicksWithTheMinimalLoginPath(t *testing.T) {
	stubs := testharness.New(t)
	stubs.SetFixture(t, eligibleWorld(true))
	path := writeConfig(t, nil)
	t.Setenv("PATH", t.TempDir())

	tr, err := prepare(context.Background(), path, []string{stubs.Dir})
	if err != nil {
		t.Fatalf("refused to start with the minimal login PATH: %v", err)
	}
	defer tr.close()
	if s := tickOnce(t, tr); s != core.StatusOK {
		t.Errorf("status after the first Tick = %s, want ok", s)
	}

	if wakes := orcaWakes(t, stubs); len(wakes) != 1 {
		t.Errorf("Wakes = %q, want the Owned Issue Woken once", wakes)
	}
}

// crashEnv names the config a re-run of this test binary ticks with and then
// crashes on, holding every lock and file it had open.
const crashEnv = "WORK_AGENT_TRAY_CRASH_CONFIG"

func TestHelperTrayCrashesAfterATick(t *testing.T) {
	path := os.Getenv(crashEnv)
	if path == "" {
		t.Skip("only runs as the crashing tray app")
	}
	tr, err := prepare(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := tickOnce(t, tr); s != core.StatusOK {
		t.Fatalf("status before the crash = %s, want ok", s)
	}
	os.Exit(3) // no close: the instance lock and log are left to the OS
}

// No supervisor restarts the tray app after a crash (ADR-0004); the Operator
// starts it again, and its first Tick reconciles with what the crashed
// instance already did.
func TestAManualRestartAfterACrashCreatesNoDuplicateWorkspacesOrWakes(t *testing.T) {
	path, stubs := configWith(t, eligibleWorld(true))
	crashed := exec.Command(os.Args[0], "-test.run=^TestHelperTrayCrashesAfterATick$")
	crashed.Env = append(os.Environ(), crashEnv+"="+path)
	out, err := crashed.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
		t.Fatalf("the tray app did not crash after a Tick: %v\n%s", err, out)
	}
	if wakes := orcaWakes(t, stubs); len(wakes) != 1 {
		t.Fatalf("before the crash: Wakes = %q, want 1", wakes)
	}

	tr, err := prepare(context.Background(), path, nil)
	if err != nil {
		t.Fatalf("restart after a crash: %v", err)
	}
	defer tr.close()
	if s := tickOnce(t, tr); s != core.StatusOK {
		t.Errorf("status after the restarted Tick = %s, want ok", s)
	}

	if wts := stubs.OrcaWorktrees(t); len(wts) != 1 {
		t.Errorf("Workspaces = %+v, want the one created before the crash", wts)
	}
	if wakes := orcaWakes(t, stubs); len(wakes) != 1 {
		t.Errorf("Wakes = %q, want only the one from before the crash", wakes)
	}
}
