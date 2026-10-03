package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// distractors are PRs in org/a that are not for org/a#1: near-miss numbers,
// a mention without a closing keyword, and the Operator's other work.
func distractors() []testharness.PullRequest {
	return []testharness.PullRequest{
		{Number: 50, State: "OPEN", HeadRefName: "issue-12", Body: "Fixes #12", Author: operator},
		{Number: 51, State: "MERGED", HeadRefName: "11-near-miss", Body: "Closes org/a#11", Author: operator, ClosingIssues: []string{"org/a#11"}},
		{Number: 52, State: "CLOSED", HeadRefName: "chore/deps", Body: "Related to #1, but closes nothing", Author: operator},
		{Number: 53, State: "MERGED", HeadRefName: "feat/other", Body: "Refactor the logger.", Author: operator},
	}
}

func withPRs(fx testharness.Fixture, prs ...testharness.PullRequest) testharness.Fixture {
	fx.PullRequests = map[string][]testharness.PullRequest{"org/a": append(distractors(), prs...)}
	return fx
}

// prFor1 is the Operator's draft PR for org/a#1, opened from its Workspace.
func prFor1(state string) testharness.PullRequest {
	return testharness.PullRequest{Number: 60, State: state, HeadRefName: "issue-1", Body: "Work on the issue.", Author: operator}
}

func closeIssue(fx testharness.Fixture, repo string, number int) testharness.Fixture {
	for i, is := range fx.Issues[repo] {
		if is.Number == number {
			fx.Issues[repo][i].State = "CLOSED"
		}
	}
	return fx
}

func TestAPRForTheIssueIsLinkedToItsWorkItem(t *testing.T) {
	c := newCLI(t, withPRs(world()))
	c.mustTick(t)
	if got := c.item(t, "org/a#1"); got.State != workflow.InProgress || got.PR != 0 {
		t.Fatalf("org/a#1 = %s with PR %d before any PR for it; near misses and unrelated PRs must not link", got.State, got.PR)
	}

	c.stubs.SetFixture(t, withPRs(world(), prFor1("OPEN")))
	stdout := c.mustTick(t)

	a := c.item(t, "org/a#1")
	if a.State != workflow.WaitingForCI || a.PR != 60 || a.PRURL != "https://github.com/org/a/pull/60" {
		t.Errorf("org/a#1 = %s with PR %d (%s), want WAITING_FOR_CI with PR 60", a.State, a.PR, a.PRURL)
	}
	if b := c.item(t, "org/b#7"); b.State != workflow.InProgress || b.PR != 0 {
		t.Errorf("org/b#7 = %s with PR %d; another repo's PRs must not link", b.State, b.PR)
	}
	if !strings.Contains(stdout, "LINK_PR\torg/a#1\tWAITING_FOR_CI") {
		t.Errorf("stdout does not report the link:\n%s", stdout)
	}
	if got := wakes(t, c); len(got) != 2 {
		t.Errorf("Wakes = %q; linking a PR must not Wake Claude", got)
	}

	linked := c.savedState(t)
	if c.mustTick(t); !reflect.DeepEqual(c.savedState(t), linked) {
		t.Error("re-observing the linked PR changed state")
	}
}

func TestAPROpenedBySomeoneElseIsNotLinked(t *testing.T) {
	pr := prFor1("OPEN")
	pr.Author = "other"
	c := newCLI(t, withPRs(world(), pr))

	c.mustTick(t)

	if got := c.item(t, "org/a#1"); got.State != workflow.InProgress || got.PR != 0 {
		t.Errorf("org/a#1 = %s with PR %d; only the Operator's PRs are linked", got.State, got.PR)
	}
}

func TestAMergedPRMovesTheItemToDoneWithoutWakingClaude(t *testing.T) {
	c := newCLI(t, withPRs(world(), prFor1("OPEN")))
	c.mustTick(t)
	c.mustTick(t)
	wakesBefore := wakes(t, c)

	// Merging closes the issue through its closing keyword, so it also
	// drops out of the Eligible issues.
	c.stubs.SetFixture(t, closeIssue(withPRs(world(), prFor1("MERGED")), "org/a", 1))
	stdout := c.mustTick(t)

	if got := c.item(t, "org/a#1"); got.State != workflow.Done || got.PR != 60 || got.Pause != nil {
		t.Errorf("org/a#1 = %s with PR %d, pause %+v; want DONE with PR 60", got.State, got.PR, got.Pause)
	}
	if !strings.Contains(stdout, "DONE\torg/a#1") {
		t.Errorf("stdout does not report DONE:\n%s", stdout)
	}
	c.mustTick(t)
	if got := wakes(t, c); !reflect.DeepEqual(got, wakesBefore) {
		t.Errorf("Wakes went from %q to %q; DONE must not Wake Claude", wakesBefore, got)
	}
}

func TestAClosedPRMovesTheItemToDone(t *testing.T) {
	c := newCLI(t, withPRs(world(), prFor1("OPEN")))
	c.mustTick(t)

	c.stubs.SetFixture(t, withPRs(world(), prFor1("CLOSED")))
	c.mustTick(t)

	if got := c.item(t, "org/a#1"); got.State != workflow.Done || got.PR != 60 {
		t.Errorf("org/a#1 = %s with PR %d, want DONE with PR 60", got.State, got.PR)
	}
}

func TestAClosedIssueMovesTheItemToDoneNotPaused(t *testing.T) {
	c := newCLI(t, world())
	c.mustTick(t)

	c.stubs.SetFixture(t, closeIssue(world(), "org/a", 1))
	c.mustTick(t)

	if got := c.item(t, "org/a#1"); got.State != workflow.Done || got.Pause != nil {
		t.Errorf("org/a#1 = %s, pause %+v; a closed issue is DONE, not Paused", got.State, got.Pause)
	}
	if got := c.item(t, "org/b#7"); got.State != workflow.InProgress {
		t.Errorf("org/b#7 = %s; only the closed issue is DONE", got.State)
	}

	// Reopening is out of scope: DONE stays DONE.
	c.stubs.SetFixture(t, world())
	c.mustTick(t)
	if got := c.item(t, "org/a#1"); got.State != workflow.Done {
		t.Errorf("org/a#1 = %s after reopening, want still DONE", got.State)
	}
}

// Each run() is a fresh process, so the PR opening and merging between two
// Ticks is a merge while the daemon was stopped.
func TestAMergeWhileTheDaemonWasStoppedIsReconciledOnTheNextTick(t *testing.T) {
	c := newCLI(t, withPRs(world()))
	c.mustTick(t)
	if got := c.item(t, "org/a#1"); got.State != workflow.InProgress {
		t.Fatalf("org/a#1 = %s, want IN_PROGRESS", got.State)
	}

	c.stubs.SetFixture(t, closeIssue(withPRs(world(), prFor1("MERGED")), "org/a", 1))
	c.mustTick(t)

	if got := c.item(t, "org/a#1"); got.State != workflow.Done || got.PR != 60 {
		t.Errorf("org/a#1 = %s with PR %d, want DONE with the merged PR 60 linked", got.State, got.PR)
	}
}

func TestDryRunReportsPRLinksAndWritesNothing(t *testing.T) {
	c := newCLI(t, world())
	c.mustTick(t)
	c.stubs.SetFixture(t, withPRs(world(), prFor1("OPEN")))
	before := snapshot(t, c.stateDir())

	stdout := c.mustTick(t, "--dry-run")

	if !strings.Contains(stdout, "LINK_PR\torg/a#1") {
		t.Errorf("dry run does not report the link:\n%s", stdout)
	}
	if after := snapshot(t, c.stateDir()); !reflect.DeepEqual(after, before) {
		t.Error("--dry-run wrote to the state directory")
	}
}
