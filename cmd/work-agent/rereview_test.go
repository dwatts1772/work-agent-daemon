package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// withPR333 is withReviewRequest with PR 333 changed by change.
func withPR333(sha string, change func(*testharness.PullRequest)) testharness.Fixture {
	fx := withReviewRequest(sha, passed)
	prs := fx.PullRequests["org/a"]
	for i := range prs {
		if prs[i].Number == 333 {
			change(&prs[i])
		}
	}
	return fx
}

// reviewing is a CLI whose Review Request org/a#333 has been Woken for head
// rrr.
func reviewing(t *testing.T) *cli {
	t.Helper()
	c := newCLI(t, withReviewRequest("rrr", passed))
	c.mustTick(t)
	if got := reviewItems(t, c); len(got) != 1 || got[0].State != workflow.Reviewing || len(reviewWakes(t, c)) != 1 {
		t.Fatalf("Review Requests = %+v, want org/a#333 REVIEWING after one review Wake", got)
	}
	return c
}

// quietPeriodPasses moves the daemon's record of when it saw each Review
// Request's head back past the Quiet Period, as if that much time had gone
// by without a push.
func quietPeriodPasses(t *testing.T, c *cli) {
	t.Helper()
	st := c.savedState(t)
	for i := range st.Items {
		st.Items[i].HeadSeenAt = st.Items[i].HeadSeenAt.Add(-time.Hour)
	}
	store, err := state.Open(c.stateDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
}

// pullRefFetches returns the fetches of PR 333's pull ref.
func pullRefFetches(t *testing.T, c *cli) int {
	t.Helper()
	n := 0
	for _, call := range c.stubs.Calls(t) {
		if call.Bin == "git" && slices.Contains(call.Args, "+refs/pull/333/head:refs/remotes/origin/pr/333") {
			n++
		}
	}
	return n
}

func TestANewHeadWhileRequestedWakesTheExistingReviewWorkspaceOnce(t *testing.T) {
	c := reviewing(t)
	ws := *reviewItems(t, c)[0].Workspace

	c.stubs.SetFixture(t, withReviewRequest("sss", passed))
	c.mustTick(t)
	if got := reviewWakes(t, c); len(got) != 1 {
		t.Fatalf("review Wakes = %q inside the Quiet Period of the push", got)
	}

	quietPeriodPasses(t, c)
	stdout := c.mustTick(t)

	wakes := reviewWakes(t, c)
	if len(wakes) != 2 || !strings.Contains(wakes[1], ws.ClaudeSessionID) || !strings.Contains(wakes[1], `"/work-item review org/a#333"`) {
		t.Fatalf("review Wakes = %q, want a second review Wake in session %s", wakes, ws.ClaudeSessionID)
	}
	if got := pullRefFetches(t, c); got != 2 {
		t.Errorf("pull ref fetched %d times, want once more for the new head", got)
	}
	if got := reviewWorktrees(t, c); len(got) != 1 {
		t.Errorf("Review Workspaces = %+v, want the one Review Workspace reused", got)
	}
	item := reviewItems(t, c)[0]
	if item.State != workflow.Reviewing || item.HeadSHA != "sss" || *item.Workspace != ws {
		t.Errorf("Review Request = %s at %q in %+v, want REVIEWING sss in %+v", item.State, item.HeadSHA, item.Workspace, ws)
	}
	if !strings.Contains(stdout, "REVIEW_DUE\torg/a#333") {
		t.Errorf("stdout does not report the re-review:\n%s", stdout)
	}

	c.mustTick(t)
	quietPeriodPasses(t, c)
	c.mustTick(t)
	if got := reviewWakes(t, c); len(got) != 2 {
		t.Errorf("review Wakes = %q, want exactly one for head sss", got)
	}
}

func TestRapidPushesInsideTheQuietPeriodProduceOneReReview(t *testing.T) {
	c := reviewing(t)

	for _, sha := range []string{"sss", "ttt", "uuu"} {
		c.stubs.SetFixture(t, withReviewRequest(sha, passed))
		c.mustTick(t)
	}
	if got := reviewWakes(t, c); len(got) != 1 {
		t.Fatalf("review Wakes = %q while the pushes are still coming", got)
	}

	quietPeriodPasses(t, c)
	c.mustTick(t)
	c.mustTick(t)

	if got := reviewWakes(t, c); len(got) != 2 {
		t.Errorf("review Wakes = %q, want one re-review for all three pushes", got)
	}
	if item := reviewItems(t, c)[0]; item.HeadSHA != "uuu" || item.State != workflow.Reviewing {
		t.Errorf("Review Request = %s at %q, want REVIEWING uuu", item.State, item.HeadSHA)
	}
}

func TestARequestClearedByTheOperatorsReviewLeavesItReviewedUntilReRequested(t *testing.T) {
	c := reviewing(t)

	// The Operator submits the review Claude prepared; GitHub clears the
	// request.
	c.stubs.SetFixture(t, withPR333("rrr", func(pr *testharness.PullRequest) {
		pr.ReviewRequests = nil
		pr.Reviews = []testharness.Review{reviewBy("PRR_1", operator, "MEMBER", "COMMENTED")}
	}))
	stdout := c.mustTick(t)
	quietPeriodPasses(t, c)
	c.mustTick(t)

	if got := reviewItems(t, c); len(got) != 1 || got[0].State != workflow.Reviewed {
		t.Fatalf("Review Requests = %+v, want org/a#333 REVIEWED", got)
	}
	if !strings.Contains(stdout, "REVIEW_SUBMITTED\torg/a#333") {
		t.Errorf("stdout does not report the Operator's review:\n%s", stdout)
	}
	if got := reviewWakes(t, c); len(got) != 1 {
		t.Fatalf("review Wakes = %q after the Operator's review, want no new one", got)
	}

	// The author asks for another look at the same head.
	c.stubs.SetFixture(t, withPR333("rrr", func(pr *testharness.PullRequest) {
		pr.Reviews = []testharness.Review{reviewBy("PRR_1", operator, "MEMBER", "COMMENTED")}
	}))
	c.mustTick(t)
	if got := reviewWakes(t, c); len(got) != 1 {
		t.Fatalf("review Wakes = %q inside the Quiet Period of the re-request", got)
	}
	quietPeriodPasses(t, c)
	c.mustTick(t)

	if got := reviewWakes(t, c); len(got) != 2 {
		t.Errorf("review Wakes = %q, want one re-review for the re-request", got)
	}
	if got := reviewItems(t, c); len(got) != 1 || got[0].State != workflow.Reviewing {
		t.Errorf("Review Requests = %+v, want org/a#333 REVIEWING again", got)
	}
}

func TestARequestRemovedBySomeoneElseOrAnEndedPRIsDoneWithoutAWake(t *testing.T) {
	for name, change := range map[string]func(*testharness.PullRequest){
		"removed by someone else": func(pr *testharness.PullRequest) { pr.ReviewRequests = nil },
		"removed after an earlier review": func(pr *testharness.PullRequest) {
			pr.ReviewRequests = nil
			r := reviewBy("PRR_0", operator, "MEMBER", "COMMENTED")
			r.SubmittedAt = r.SubmittedAt.Add(-24 * time.Hour)
			pr.Reviews = []testharness.Review{r}
		},
		"merged": func(pr *testharness.PullRequest) { pr.State = "MERGED" },
		"closed": func(pr *testharness.PullRequest) { pr.State = "CLOSED" },
	} {
		t.Run(name, func(t *testing.T) {
			c := reviewing(t)
			ws := *reviewItems(t, c)[0].Workspace

			c.stubs.SetFixture(t, withPR333("sss", change))
			c.mustTick(t)
			quietPeriodPasses(t, c)
			c.mustTick(t)

			if got := reviewItems(t, c); len(got) != 1 || got[0].State != workflow.Done || *got[0].Workspace != ws {
				t.Errorf("Review Requests = %+v, want org/a#333 DONE with its Workspace kept", got)
			}
			if got := reviewWakes(t, c); len(got) != 1 {
				t.Errorf("review Wakes = %q, want none after the first", got)
			}
			if got := reviewWorktrees(t, c); len(got) != 1 {
				t.Errorf("Review Workspaces = %+v, want the Review Workspace retained", got)
			}
		})
	}
}
