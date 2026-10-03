package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// issueWakes returns the items named by the issue Wakes sent to Orca.
func issueWakes(t *testing.T, c *cli) []string {
	t.Helper()
	var got []string
	for _, cmd := range wakes(t, c) {
		if _, ref, ok := strings.Cut(cmd, " issue "); ok {
			got = append(got, strings.TrimSuffix(ref, `"`))
		}
	}
	return got
}

// withAgents sets the states of the agents in the Workspaces Orca names.
func withAgents(fx testharness.Fixture, states map[string][]string) testharness.Fixture {
	fx.Orca.AgentStates = states
	return fx
}

func TestASecondOwnedIssueWakeIsHeldForCapacityWhileTheFirstAgentIsWorking(t *testing.T) {
	c := newCLIWithCapacity(t, world(), nil)
	c.mustTick(t)

	if got := issueWakes(t, c); !slices.Equal(got, []string{"org/a#1"}) {
		t.Fatalf("issue Wakes = %q, want only org/a#1 in the one Owned Issue slot", got)
	}
	held := c.item(t, "org/b#7")
	if held.HeldWake == nil || held.HeldWake.Why != workflow.HoldCapacity || held.HeldWake.Reason != workflow.WakeIssue {
		t.Fatalf("org/b#7 HeldWake = %+v, want its issue Wake Held for capacity", held.HeldWake)
	}
	if held.Workspace != nil || len(c.stubs.OrcaWorktrees(t)) != 1 {
		t.Errorf("a Workspace was created for the Held org/b#7: %+v", held.Workspace)
	}

	c.stubs.SetFixture(t, withAgents(world(), map[string][]string{"issue-1": {"working"}}))
	c.mustTick(t)
	c.mustTick(t)
	if got := issueWakes(t, c); len(got) != 1 {
		t.Fatalf("issue Wakes = %q while org/a#1's agent is working, want still one", got)
	}
	if got := c.item(t, "org/b#7"); got.HeldWake == nil || got.HeldWake.Why != workflow.HoldCapacity || !got.HeldWake.Since.Equal(held.HeldWake.Since) {
		t.Errorf("org/b#7 HeldWake = %+v, want still Held for capacity since %v", got.HeldWake, held.HeldWake.Since)
	}
}

func TestAnIdleOrWaitingAgentFreesItsSlot(t *testing.T) {
	for _, state := range []string{"idle", "waiting"} {
		c := newCLIWithCapacity(t, withAgents(world(), map[string][]string{"issue-1": {"working"}}), nil)
		c.mustTick(t)
		c.mustTick(t)
		if got := issueWakes(t, c); len(got) != 1 {
			t.Fatalf("issue Wakes = %q while org/a#1's agent is working, want one", got)
		}

		c.stubs.SetFixture(t, withAgents(world(), map[string][]string{"issue-1": {state}}))
		c.mustTick(t)

		if got := issueWakes(t, c); !slices.Equal(got, []string{"org/a#1", "org/b#7"}) {
			t.Errorf("with org/a#1's agent %s, issue Wakes = %q, want org/b#7 released", state, got)
		}
		if got := c.item(t, "org/b#7"); got.State != workflow.InProgress || got.HeldWake != nil {
			t.Errorf("org/b#7 = %s with HeldWake %+v, want IN_PROGRESS", got.State, got.HeldWake)
		}
	}
}

func TestAReviewRequestWakeProceedsWhileAnOwnedIssueAgentIsWorking(t *testing.T) {
	c := newCLIWithCapacity(t, world(), nil)
	c.mustTick(t)

	fx := withReviewRequest("rrr", passed)
	fx.Orca.AgentStates = map[string][]string{"issue-1": {"working"}}
	c.stubs.SetFixture(t, fx)
	c.mustTick(t)

	if got := reviewWakes(t, c); len(got) != 1 {
		t.Errorf("review Wakes = %q, want the Review Request Woken beside the working Owned Issue agent", got)
	}
	if got := c.item(t, "org/a#333"); got.State != workflow.Reviewing {
		t.Errorf("org/a#333 = %s with HeldWake %+v, want REVIEWING", got.State, got.HeldWake)
	}
}
