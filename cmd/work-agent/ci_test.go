package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

var (
	running = testharness.Check{Name: "test", Status: "IN_PROGRESS"}
	passed  = testharness.Check{Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS"}
	failed  = testharness.Check{Name: "test", Status: "COMPLETED", Conclusion: "FAILURE"}
)

// withCI is the world with the Operator's open PR 60 for org/a#1 at head
// sha, its checks in the given states.
func withCI(sha string, checks ...testharness.Check) testharness.Fixture {
	pr := prFor1("OPEN")
	pr.HeadSHA, pr.Checks = sha, checks
	return withPRs(world(), pr)
}

// withAgent sets the states of org/a#1's agents and its live terminals.
func withAgent(fx testharness.Fixture, states []string, terminals ...testharness.OrcaTerminal) testharness.Fixture {
	fx.Orca.AgentStates = map[string][]string{"issue-1": states}
	fx.Orca.Terminals = map[string][]testharness.OrcaTerminal{"issue-1": terminals}
	return fx
}

// ciFailureWakes returns the commands of the ci-failure Wakes sent to Orca:
// Claude started in a new terminal, or the prompt sent into a live one.
func ciFailureWakes(t *testing.T, c *cli) []string {
	t.Helper()
	var got []string
	for _, call := range c.stubs.Calls(t) {
		if call.Bin != "orca" || len(call.Args) < 2 || call.Args[0] != "terminal" {
			continue
		}
		for _, a := range call.Args {
			if strings.Contains(a, "ci-failure") {
				got = append(got, call.Args[1]+" "+a)
			}
		}
	}
	return got
}

// haveTranscript makes Claude's transcript of org/a#1's session exist, as it
// does once Claude has run in its Workspace.
func haveTranscript(t *testing.T, c *cli) {
	t.Helper()
	ws := c.item(t, "org/a#1").Workspace
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "C--ws-issue-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ws.ClaudeSessionID+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestASettledCIFailureWakesTheSameWorkspaceOncePerHeadSHA(t *testing.T) {
	c := newCLI(t, withCI("aaa", running, passed))
	c.mustTick(t)
	haveTranscript(t, c)
	session := c.item(t, "org/a#1").Workspace.ClaudeSessionID

	c.mustTick(t)
	if got := ciFailureWakes(t, c); len(got) != 0 {
		t.Fatalf("Wakes %q while checks are still running", got)
	}
	if got := c.item(t, "org/a#1"); got.State != workflow.WaitingForCI || got.HeadSHA != "aaa" {
		t.Fatalf("org/a#1 = %s at %q, want WAITING_FOR_CI at aaa", got.State, got.HeadSHA)
	}

	c.stubs.SetFixture(t, withCI("aaa", failed, passed))
	stdout := c.mustTick(t)

	want := []string{`create claude --resume ` + session + ` "/work-item ci-failure org/a#60"`}
	if got := ciFailureWakes(t, c); !slices.Equal(got, want) {
		t.Fatalf("Wakes =\n  %q\nwant\n  %q", got, want)
	}
	if got := c.item(t, "org/a#1"); got.State != workflow.AddressingFeedback || got.Workspace.ClaudeSessionID != session {
		t.Errorf("org/a#1 = %s with session %s, want ADDRESSING_FEEDBACK in the same session", got.State, got.Workspace.ClaudeSessionID)
	}
	if !strings.Contains(stdout, "CI_FAILED\torg/a#1") {
		t.Errorf("stdout does not report the failure:\n%s", stdout)
	}
	if wts := c.stubs.OrcaWorktrees(t); len(wts) != 2 {
		t.Errorf("Orca worktrees = %+v; the Wake must reuse the Workspace", wts)
	}

	// Re-observing the same Settled failure does nothing.
	c.mustTick(t)
	c.mustTick(t)
	if got := ciFailureWakes(t, c); len(got) != 1 {
		t.Errorf("Wakes = %q, want exactly one for head aaa", got)
	}

	// Claude pushes a fix: the new head waits for CI, and its own failure
	// Wakes once more.
	c.stubs.SetFixture(t, withCI("bbb", running))
	c.mustTick(t)
	if got := c.item(t, "org/a#1"); got.State != workflow.WaitingForCI || got.HeadSHA != "bbb" {
		t.Errorf("org/a#1 = %s at %q after a push, want WAITING_FOR_CI at bbb", got.State, got.HeadSHA)
	}
	c.stubs.SetFixture(t, withCI("bbb", failed))
	c.mustTick(t)
	c.mustTick(t)
	if got := ciFailureWakes(t, c); len(got) != 2 {
		t.Errorf("Wakes = %q, want one per head SHA", got)
	}
}

func TestACIFailureWakeStartsAFreshSessionWhenTheConversationCannotBeResumed(t *testing.T) {
	c := newCLI(t, withCI("aaa", failed))
	c.mustTick(t) // creates the Workspace and records the PR; no transcript
	c.mustTick(t)

	session := c.item(t, "org/a#1").Workspace.ClaudeSessionID
	want := []string{`create claude --session-id ` + session + ` "/work-item ci-failure org/a#60"`}
	if got := ciFailureWakes(t, c); !slices.Equal(got, want) {
		t.Errorf("Wakes =\n  %q\nwant\n  %q", got, want)
	}
}

func TestStillRunningChecksNeverWake(t *testing.T) {
	c := newCLI(t, withCI("aaa", failed, running))
	for range 3 {
		c.mustTick(t)
	}
	if got := ciFailureWakes(t, c); len(got) != 0 {
		t.Errorf("Wakes %q while a check is still running", got)
	}
}

func TestACIFailureWakeIsHeldWhileTheAgentIsWorkingAcrossRestarts(t *testing.T) {
	c := newCLI(t, withCI("aaa", running))
	c.mustTick(t)
	c.mustTick(t)
	haveTranscript(t, c)

	working := withAgent(withCI("aaa", failed), []string{"working"}, testharness.OrcaTerminal{Handle: "term_claude", AgentIdentity: "claude"})
	c.stubs.SetFixture(t, working)
	c.mustTick(t)

	// Every run is a fresh process, so each Tick below is after a restart.
	for range 2 {
		got := c.item(t, "org/a#1")
		if got.HeldWake == nil || got.HeldWake.Why != workflow.HoldAgentWorking || got.HeldWake.Reason != workflow.WakeCIFailure {
			t.Fatalf("HeldWake = %+v, want a ci-failure Wake Held for agent-working", got.HeldWake)
		}
		if got.State != workflow.WaitingForCI {
			t.Errorf("org/a#1 = %s while Held, want WAITING_FOR_CI", got.State)
		}
		if w := ciFailureWakes(t, c); len(w) != 0 {
			t.Fatalf("Woke a working agent: %q", w)
		}
		c.mustTick(t)
	}

	c.stubs.SetFixture(t, withAgent(withCI("aaa", failed), []string{"idle"}, testharness.OrcaTerminal{Handle: "term_claude", AgentIdentity: "claude"}))
	c.mustTick(t)
	c.mustTick(t)

	want := []string{`send /work-item ci-failure org/a#60`}
	if got := ciFailureWakes(t, c); !slices.Equal(got, want) {
		t.Errorf("Wakes =\n  %q\nwant the prompt sent once into the live idle Claude\n  %q", got, want)
	}
	if got := c.item(t, "org/a#1"); got.State != workflow.AddressingFeedback || got.HeldWake != nil {
		t.Errorf("org/a#1 = %s with HeldWake %+v; want ADDRESSING_FEEDBACK, released", got.State, got.HeldWake)
	}
}

func TestACIFailureWakeIsHeldWhileClaudeIsWaitingOnTheOperator(t *testing.T) {
	c := newCLI(t, withCI("aaa", running))
	c.mustTick(t)
	c.mustTick(t)

	c.stubs.SetFixture(t, withAgent(withCI("aaa", failed), []string{"waiting"}, testharness.OrcaTerminal{Handle: "term_claude", AgentIdentity: "claude"}))
	c.mustTick(t)

	if w := ciFailureWakes(t, c); len(w) != 0 {
		t.Errorf("Woke a Claude waiting on the Operator: %q", w)
	}
	if got := c.item(t, "org/a#1"); got.HeldWake == nil || got.HeldWake.Reason != workflow.WakeCIFailure || got.State != workflow.WaitingForCI {
		t.Errorf("org/a#1 = %s with HeldWake %+v, want the ci-failure Wake Held", got.State, got.HeldWake)
	}
}
