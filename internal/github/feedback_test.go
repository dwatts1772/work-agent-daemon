package github

import (
	"testing"
	"time"
)

var at = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

func ids(fs []Feedback) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.ID)
	}
	return out
}

func TestOnlyWriteAccessAuthorsAndFeedbackBotsGiveFeedback(t *testing.T) {
	comments := []Comment{
		{ID: "owner", Author: "alice", AuthorAssociation: "OWNER", Body: "Rename this.", CreatedAt: at},
		{ID: "member", Author: "bob", AuthorAssociation: "MEMBER", Body: "Add a test.", CreatedAt: at},
		{ID: "collab", Author: "carol", AuthorAssociation: "COLLABORATOR", Body: "Typo.", CreatedAt: at},
		{ID: "contributor", Author: "dave", AuthorAssociation: "CONTRIBUTOR", Body: "Please also fix X.", CreatedAt: at},
		{ID: "drive-by", Author: "eve", AuthorAssociation: "NONE", Body: "Do this instead.", CreatedAt: at},
		{ID: "bot", Author: "coderabbitai", AuthorAssociation: "NONE", Body: "Nitpick.", CreatedAt: at},
		{ID: "other-bot", Author: "spambot", AuthorAssociation: "NONE", Body: "Buy now.", CreatedAt: at},
		{ID: "operator", Author: "Op", AuthorAssociation: "OWNER", Body: "Addressed in abc123.", CreatedAt: at},
	}
	reviews := []Review{
		{ID: "r-member", Author: "bob", AuthorAssociation: "MEMBER", State: "CHANGES_REQUESTED", SubmittedAt: at},
		{ID: "r-none", Author: "eve", AuthorAssociation: "NONE", State: "COMMENTED", SubmittedAt: at},
		{ID: "r-bot", Author: "coderabbitai[bot]", AuthorAssociation: "NONE", State: "COMMENTED", SubmittedAt: at},
	}

	got, _ := Triage("op", []string{"coderabbitai[bot]"}, reviews, comments)

	want := []string{"r-member", "r-bot", "owner", "member", "collab", "bot"}
	if g := ids(got); len(g) != len(want) {
		t.Fatalf("feedback = %q, want %q", g, want)
	} else {
		for i := range want {
			if g[i] != want[i] {
				t.Fatalf("feedback = %q, want %q", g, want)
			}
		}
	}
	if !got[0].Review || got[2].Review || !got[2].At.Equal(at) {
		t.Errorf("feedback = %+v; reviews and comments must be told apart, with their times", got)
	}
}

func TestQuotedTextAndCodeFencesAreNotFeedback(t *testing.T) {
	for body, counts := range map[string]bool{
		"> Rename this.\n":                       false,
		"> quoted\n>more quoted\n\n   ":          false,
		"```go\nfunc x() {}\n```":                false,
		"~~~\nlog output\n~~~\n> and a quote":    false,
		"":                                       false,
		"> Rename this.\n\nDone, but why?":       true,
		"```\ncode\n```\nThis fails on Windows.": true,
	} {
		got, _ := Triage("op", nil, nil, []Comment{{ID: "c", Author: "alice", AuthorAssociation: "OWNER", Body: body, CreatedAt: at}})
		if (len(got) == 1) != counts {
			t.Errorf("body %q: counts as feedback = %v, want %v", body, len(got) == 1, counts)
		}
	}
}

func TestOnlyReviewsAskingForSomethingAreFeedback(t *testing.T) {
	for state, counts := range map[string]bool{"COMMENTED": true, "CHANGES_REQUESTED": true, "APPROVED": false, "DISMISSED": false, "PENDING": false} {
		got, _ := Triage("op", nil, []Review{{ID: "r", Author: "alice", AuthorAssociation: "OWNER", State: state, SubmittedAt: at}}, nil)
		if (len(got) == 1) != counts {
			t.Errorf("%s review counts as feedback = %v, want %v", state, len(got) == 1, counts)
		}
	}
}

func TestAChangeRequestStandsUntilItsReviewerApprovesOrItIsDismissed(t *testing.T) {
	r := func(author, assoc, state string) Review {
		return Review{ID: author + state, Author: author, AuthorAssociation: assoc, State: state, SubmittedAt: at}
	}
	for name, tc := range map[string]struct {
		reviews []Review
		want    bool
	}{
		"no reviews":                    {nil, false},
		"approved":                      {[]Review{r("alice", "OWNER", "APPROVED")}, false},
		"changes requested":             {[]Review{r("alice", "OWNER", "CHANGES_REQUESTED")}, true},
		"approved after changes":        {[]Review{r("alice", "OWNER", "CHANGES_REQUESTED"), r("alice", "OWNER", "APPROVED")}, false},
		"a comment keeps the request":   {[]Review{r("alice", "OWNER", "CHANGES_REQUESTED"), r("alice", "OWNER", "COMMENTED")}, true},
		"changes after approval":        {[]Review{r("alice", "OWNER", "APPROVED"), r("alice", "OWNER", "CHANGES_REQUESTED")}, true},
		"another reviewer objects":      {[]Review{r("alice", "OWNER", "APPROVED"), r("bob", "MEMBER", "CHANGES_REQUESTED")}, true},
		"change request dismissed":      {[]Review{r("alice", "OWNER", "CHANGES_REQUESTED"), r("alice", "OWNER", "DISMISSED")}, false},
		"drive-by change request":       {[]Review{r("eve", "NONE", "CHANGES_REQUESTED")}, false},
		"the Operator's change request": {[]Review{r("op", "OWNER", "CHANGES_REQUESTED")}, false},
	} {
		if _, got := Triage("op", nil, tc.reviews, nil); got != tc.want {
			t.Errorf("%s: changes requested = %v, want %v", name, got, tc.want)
		}
	}
}
