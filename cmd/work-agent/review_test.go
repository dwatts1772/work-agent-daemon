package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// withReviewRequest is the world with another developer's open PR 333 in
// org/a requesting the Operator's review, at head sha with the given
// checks, beside PRs that do not request the Operator explicitly.
func withReviewRequest(sha string, checks ...testharness.Check) testharness.Fixture {
	fx := withPRs(world(),
		testharness.PullRequest{Number: 333, State: "OPEN", Title: "Alice's change", HeadRefName: "feature", Author: "alice", ReviewRequests: []string{operator}, HeadSHA: sha, Checks: checks},
		testharness.PullRequest{Number: 334, State: "OPEN", Title: "Team review", HeadRefName: "team", Author: "bob", ReviewRequests: []string{"org/reviewers"}, HeadSHA: "ttt", Checks: []testharness.Check{passed}},
		testharness.PullRequest{Number: 335, State: "OPEN", Title: "Someone else's review", HeadRefName: "else", Author: "bob", ReviewRequests: []string{"other"}, HeadSHA: "eee", Checks: []testharness.Check{passed}},
		testharness.PullRequest{Number: 336, State: "CLOSED", Title: "Closed", HeadRefName: "gone", Author: "bob", ReviewRequests: []string{operator}, HeadSHA: "ccc", Checks: []testharness.Check{passed}},
	)
	fx.PullRequests["org/not-allowlisted"] = []testharness.PullRequest{
		{Number: 9, State: "OPEN", Title: "Outside the allowlist", Author: "bob", ReviewRequests: []string{operator}, HeadSHA: "nnn", Checks: []testharness.Check{passed}},
	}
	return fx
}

// reviewItems returns the saved Review Requests.
func reviewItems(t *testing.T, c *cli) []workflow.WorkItem {
	t.Helper()
	var items []workflow.WorkItem
	for _, w := range c.savedState(t).Items {
		if w.Kind == workflow.KindReviewRequest {
			items = append(items, w)
		}
	}
	return items
}

// reviewWorktrees returns the Review Workspaces Orca has created.
func reviewWorktrees(t *testing.T, c *cli) []testharness.OrcaWorktree {
	t.Helper()
	var wts []testharness.OrcaWorktree
	for _, wt := range c.stubs.OrcaWorktrees(t) {
		if strings.HasPrefix(wt.Name, "review-") {
			wts = append(wts, wt)
		}
	}
	return wts
}

// reviewWakes returns the review Wake commands sent to Orca.
func reviewWakes(t *testing.T, c *cli) []string {
	t.Helper()
	var got []string
	for _, cmd := range wakes(t, c) {
		if strings.Contains(cmd, " review ") {
			got = append(got, cmd)
		}
	}
	return got
}

func TestAReviewRequestGetsOneReviewWorkspaceAndReviewWakeOnceCIIsSettled(t *testing.T) {
	c := newCLI(t, withReviewRequest("rrr", running, passed))
	c.mustTick(t)
	c.mustTick(t)

	if got := reviewItems(t, c); len(got) != 0 {
		t.Fatalf("Review Requests %+v tracked while CI is still running", got)
	}
	if got := reviewWorktrees(t, c); len(got) != 0 {
		t.Fatalf("Review Workspaces %+v created while CI is still running", got)
	}

	c.stubs.SetFixture(t, withReviewRequest("rrr", failed, passed))
	stdout := c.mustTick(t)

	items := reviewItems(t, c)
	if len(items) != 1 {
		t.Fatalf("Review Requests = %+v, want only org/a#333", items)
	}
	item := items[0]
	if item.ID != "org/a#333" || item.State != workflow.Reviewing || item.HeadSHA != "rrr" || item.PR != 333 {
		t.Errorf("Review Request = %s %s at %q, want org/a#333 REVIEWING at rrr", item.ID, item.State, item.HeadSHA)
	}
	wts := reviewWorktrees(t, c)
	if len(wts) != 1 || wts[0].Name != "review-pr-333" || wts[0].BaseBranch != "origin/pr/333" || wts[0].RepoID != "repo-a" {
		t.Fatalf("Review Workspaces = %+v, want review-pr-333 in repo-a from origin/pr/333", wts)
	}
	if item.Workspace == nil || item.Workspace.OrcaIdentityKey != wts[0].IdentityKey || item.Workspace.Branch != "review-pr-333" {
		t.Errorf("Workspace = %+v, want the review-pr-333 worktree", item.Workspace)
	}
	want := []string{`claude --session-id ` + item.Workspace.ClaudeSessionID + ` "/work-item review org/a#333"`}
	if got := reviewWakes(t, c); !slices.Equal(got, want) {
		t.Errorf("review Wakes =\n  %q\nwant\n  %q", got, want)
	}
	if !strings.Contains(stdout, "CREATE_REVIEW_REQUEST\torg/a#333") {
		t.Errorf("stdout does not report the Review Request:\n%s", stdout)
	}

	// Re-observing the same request and head SHA creates or Wakes nothing.
	c.mustTick(t)
	c.mustTick(t)
	if got := reviewItems(t, c); len(got) != 1 || got[0].State != workflow.Reviewing {
		t.Errorf("Review Requests = %+v, want org/a#333 still REVIEWING", got)
	}
	if got := reviewWorktrees(t, c); len(got) != 1 {
		t.Errorf("Review Workspaces = %+v, want exactly one", got)
	}
	if got := reviewWakes(t, c); len(got) != 1 {
		t.Errorf("review Wakes = %q, want exactly one", got)
	}
}

func TestAReviewWorkspaceIsBuiltFromTheFetchedPullRefAfterCIIsSettled(t *testing.T) {
	c := newCLI(t, withReviewRequest("rrr", passed))
	c.mustTick(t)

	var fetches [][]string
	createdAt := -1
	for i, call := range c.stubs.Calls(t) {
		switch {
		case call.Bin == "git":
			fetches = append(fetches, call.Args)
		case call.Bin == "orca" && slices.Contains(call.Args, "review-pr-333"):
			createdAt = i
			if len(fetches) == 0 {
				t.Errorf("review-pr-333 created before the pull ref was fetched")
			}
		}
	}
	if createdAt < 0 {
		t.Fatal("no Review Workspace created")
	}
	if len(fetches) != 1 || !slices.Contains(fetches[0], "+refs/pull/333/head:refs/remotes/origin/pr/333") {
		t.Errorf("git calls = %q, want one fetch of the pull ref into origin/pr/333", fetches)
	}
}

func TestReviewRequestsAreDiscoveredOnlyAsTheOperator(t *testing.T) {
	c := newCLI(t, withReviewRequest("rrr", passed))
	c.mustTick(t)

	for _, call := range c.stubs.Calls(t) {
		if call.Bin == "gh" && slices.Contains(call.Args, "--search") && call.Env["GH_TOKEN"] != operatorToken {
			t.Errorf("gh %q ran as %q, not the Operator", call.Args, call.Env["GH_TOKEN"])
		}
	}
}

func TestPausedReviewRequestsAreNotWoken(t *testing.T) {
	fx := withReviewRequest("rrr", passed)
	fx.Orca = nil
	c := newCLI(t, fx)
	c.mustTick(t) // Orca is down: the Review Request is tracked, its Wake Held
	c.must(t, "pause", "org/a#333")

	c.stubs.SetFixture(t, withReviewRequest("rrr", passed))
	c.mustTick(t)

	if got := reviewWorktrees(t, c); len(got) != 0 {
		t.Errorf("Review Workspaces %+v created for a Paused Review Request", got)
	}
	if got := reviewItems(t, c); len(got) != 1 || got[0].State != workflow.Paused {
		t.Errorf("Review Requests = %+v, want org/a#333 Paused", got)
	}
}
