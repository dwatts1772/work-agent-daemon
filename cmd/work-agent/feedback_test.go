package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// withFeedback is the world with the Operator's open PR 60 for org/a#1 at
// head sha with CI green, and the given reviews and comments on it.
func withFeedback(sha string, reviews []testharness.Review, comments ...testharness.Comment) testharness.Fixture {
	pr := prFor1("OPEN")
	pr.HeadSHA, pr.Checks = sha, []testharness.Check{passed}
	pr.Reviews, pr.Comments = reviews, comments
	return withPRs(world(), pr)
}

func reviewBy(id, author, association, state string) testharness.Review {
	return testharness.Review{ID: id, Author: author, AuthorAssociation: association, State: state, SubmittedAt: time.Now().UTC()}
}

func commentBy(id, author, association, body string, ago time.Duration) testharness.Comment {
	return testharness.Comment{ID: id, Author: author, AuthorAssociation: association, Body: body, CreatedAt: time.Now().UTC().Add(-ago)}
}

// feedbackWakes returns the commands of the feedback Wakes sent to Orca.
func feedbackWakes(t *testing.T, c *cli) []string {
	t.Helper()
	var got []string
	for _, call := range c.stubs.Calls(t) {
		if call.Bin != "orca" || len(call.Args) < 2 || call.Args[0] != "terminal" {
			continue
		}
		for _, a := range call.Args {
			if strings.Contains(a, " feedback ") {
				got = append(got, call.Args[1]+" "+a)
			}
		}
	}
	return got
}

// linkedAndGreen ticks c until org/a#1's PR is linked and its green CI seen,
// with Claude's transcript in place so a Wake resumes the conversation.
func linkedAndGreen(t *testing.T, c *cli) string {
	t.Helper()
	c.mustTick(t)
	haveTranscript(t, c)
	c.mustTick(t)
	if got := c.item(t, "org/a#1"); got.State != workflow.WaitingForReview {
		t.Fatalf("org/a#1 = %s, want WAITING_FOR_REVIEW: CI green, no approval yet", got.State)
	}
	return c.item(t, "org/a#1").Workspace.ClaudeSessionID
}

func TestASubmittedReviewWakesTheSameWorkspaceOnceResumingTheConversation(t *testing.T) {
	c := newCLI(t, withFeedback("aaa", nil))
	session := linkedAndGreen(t, c)

	c.stubs.SetFixture(t, withFeedback("aaa", []testharness.Review{reviewBy("PRR_1", "alice", "MEMBER", "CHANGES_REQUESTED")}))
	stdout := c.mustTick(t)

	want := []string{`create claude --resume ` + session + ` "/work-item feedback org/a#60"`}
	if got := feedbackWakes(t, c); !slices.Equal(got, want) {
		t.Fatalf("Wakes =\n  %q\nwant\n  %q", got, want)
	}
	if got := c.item(t, "org/a#1"); got.State != workflow.AddressingFeedback || got.Workspace.ClaudeSessionID != session {
		t.Errorf("org/a#1 = %s with session %s, want ADDRESSING_FEEDBACK in the same session", got.State, got.Workspace.ClaudeSessionID)
	}
	if !strings.Contains(stdout, "FEEDBACK_DUE\torg/a#1") {
		t.Errorf("stdout does not report the feedback:\n%s", stdout)
	}
	if wts := c.stubs.OrcaWorktrees(t); len(wts) != 2 {
		t.Errorf("Orca worktrees = %+v; the Wake must reuse the Workspace", wts)
	}

	// Re-observing the same review does nothing.
	c.mustTick(t)
	c.mustTick(t)
	if got := feedbackWakes(t, c); len(got) != 1 {
		t.Errorf("Wakes = %q, want exactly one for the review", got)
	}
}

func TestAQuietCommentBatchWakesOnce(t *testing.T) {
	c := newCLI(t, withFeedback("aaa", nil))
	session := linkedAndGreen(t, c)

	// Two comments, the newest just posted: still within the Quiet Period.
	c.stubs.SetFixture(t, withFeedback("aaa", nil,
		commentBy("IC_1", "alice", "OWNER", "Please rename Foo.", 8*time.Minute),
		commentBy("IC_2", "bob", "COLLABORATOR", "> Please rename Foo.\n\nAnd Bar.", 0)))
	c.mustTick(t)
	if got := feedbackWakes(t, c); len(got) != 0 {
		t.Fatalf("Woke %q before the comments went quiet", got)
	}

	// Time passes with no new comment.
	c.stubs.SetFixture(t, withFeedback("aaa", nil,
		commentBy("IC_1", "alice", "OWNER", "Please rename Foo.", 14*time.Minute),
		commentBy("IC_2", "bob", "COLLABORATOR", "> Please rename Foo.\n\nAnd Bar.", 6*time.Minute)))
	c.mustTick(t)
	c.mustTick(t)

	want := []string{`create claude --resume ` + session + ` "/work-item feedback org/a#60"`}
	if got := feedbackWakes(t, c); !slices.Equal(got, want) {
		t.Errorf("Wakes =\n  %q\nwant one for the batch\n  %q", got, want)
	}
}

func TestFeedbackFromAuthorsWithoutWriteAccessNeverWakes(t *testing.T) {
	c := newCLI(t, withFeedback("aaa", nil))
	linkedAndGreen(t, c)

	c.stubs.SetFixture(t, withFeedback("aaa",
		[]testharness.Review{
			reviewBy("PRR_1", "eve", "NONE", "CHANGES_REQUESTED"),
			reviewBy("PRR_2", "dave", "CONTRIBUTOR", "COMMENTED"),
			reviewBy("PRR_3", operator, "OWNER", "COMMENTED"),
		},
		commentBy("IC_1", "eve", "NONE", "Do it differently.", time.Hour),
		commentBy("IC_2", "renovate", "NONE", "Dependency dashboard.", time.Hour),
		commentBy("IC_3", operator, "OWNER", "Addressed in abc123.", time.Hour),
		commentBy("IC_4", "alice", "OWNER", "```\nstack trace\n```", time.Hour)))
	for range 3 {
		c.mustTick(t)
	}

	if got := feedbackWakes(t, c); len(got) != 0 {
		t.Errorf("Woke %q for feedback that does not count", got)
	}
	if got := c.item(t, "org/a#1"); got.State != workflow.WaitingForReview {
		t.Errorf("org/a#1 = %s, want WAITING_FOR_REVIEW", got.State)
	}
}

func TestAConfiguredFeedbackBotWakes(t *testing.T) {
	c := newCLI(t, withFeedback("aaa", nil))
	linkedAndGreen(t, c)

	c.stubs.SetFixture(t, withFeedback("aaa", []testharness.Review{reviewBy("PRR_1", "coderabbitai", "NONE", "COMMENTED")}))
	c.mustTick(t)

	if got := feedbackWakes(t, c); len(got) != 1 {
		t.Errorf("Wakes = %q, want one for the feedbackBots review", got)
	}
}

func TestThePRLifecycleReachesReadyToMergeAndRegressesOnANewHead(t *testing.T) {
	c := newCLI(t, withCI("aaa", running))
	c.mustTick(t)
	c.mustTick(t)
	if got := c.item(t, "org/a#1"); got.State != workflow.WaitingForCI {
		t.Fatalf("org/a#1 = %s, want WAITING_FOR_CI while checks run", got.State)
	}

	c.stubs.SetFixture(t, withFeedback("aaa", nil))
	if stdout := c.mustTick(t); !strings.Contains(stdout, "CI_PASSED\torg/a#1\tWAITING_FOR_REVIEW") {
		t.Errorf("stdout does not report the move to WAITING_FOR_REVIEW:\n%s", stdout)
	}

	approval := []testharness.Review{reviewBy("PRR_1", "alice", "OWNER", "APPROVED")}
	c.stubs.SetFixture(t, withFeedback("aaa", approval))
	c.mustTick(t)
	if got := c.item(t, "org/a#1"); got.State != workflow.ReadyToMerge {
		t.Fatalf("org/a#1 = %s, want READY_TO_MERGE once approved with CI green", got.State)
	}
	if got := feedbackWakes(t, c); len(got) != 0 {
		t.Errorf("an approval Woke Claude: %q", got)
	}
	for _, call := range c.stubs.Calls(t) {
		if call.Bin == "gh" && slices.Contains(call.Args, "merge") {
			t.Errorf("the daemon tried to merge: %q", call.Args)
		}
	}

	// A new head goes back to waiting for CI.
	c.stubs.SetFixture(t, withCI("bbb", running))
	c.mustTick(t)
	if got := c.item(t, "org/a#1"); got.State != workflow.WaitingForCI || got.HeadSHA != "bbb" {
		t.Errorf("org/a#1 = %s at %q after a push, want WAITING_FOR_CI at bbb", got.State, got.HeadSHA)
	}
}
