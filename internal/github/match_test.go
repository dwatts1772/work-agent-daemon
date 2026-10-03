package github

import "testing"

func TestPullRequestCoversIssue(t *testing.T) {
	const repo = "org/a"
	pr := func(branch, body string, closing ...string) PullRequest {
		return PullRequest{Repo: repo, Number: 100, HeadRefName: branch, Body: body, ClosingIssues: closing}
	}
	cases := []struct {
		name   string
		pr     PullRequest
		issue  int
		branch string
		want   bool
	}{
		// Branch name.
		{"the Workspace's own branch", pr("wa/issue-4", ""), 4, "wa/issue-4", true},
		{"issue-N branch", pr("issue-4", ""), 4, "", true},
		{"issue-N-slug branch", pr("feat/issue-4-add-thing", ""), 4, "", true},
		{"issues/N branch", pr("issues/4", ""), 4, "", true},
		{"a leading number is not an issue", pr("4-add-thing", ""), 4, "", false},
		{"a leading year is not an issue", pr("feat/2024-cleanup", ""), 2024, "", false},
		{"issue-NN branch is a near miss", pr("feat/issue-42-add-thing", ""), 4, "", false},
		{"issue-N branch for a longer issue", pr("feat/issue-4-add-thing", ""), 42, "", false},
		{"NN-slug branch is a near miss", pr("42-add-thing", ""), 4, "", false},
		{"digit inside a word is not an issue", pr("release-v4-notes", ""), 4, "", false},
		{"number in the middle of a branch", pr("fix-4-things", ""), 4, "", false},
		{"another Workspace's branch", pr("wa/issue-42", ""), 4, "wa/issue-4", false},

		// Closing keywords.
		{"Fixes #N", pr("x", "Fixes #4"), 4, "", true},
		{"closes: #N, lower case", pr("x", "This closes: #4."), 4, "", true},
		{"Resolved owner/name#N", pr("x", "Resolved org/a#4"), 4, "", true},
		{"closing keyword with the issue URL", pr("x", "Closes https://github.com/org/a/issues/4"), 4, "", true},
		{"one of several closed issues", pr("x", "Fixes #2, fixes #4"), 4, "", true},
		{"Fixes #NN is a near miss", pr("x", "Fixes #42"), 4, "", false},
		{"Fixes #N for a longer issue", pr("x", "Fixes #4"), 42, "", false},
		{"Fixes #N-digit prefix is a near miss", pr("x", "Fixes #401"), 40, "", false},
		{"mention without a closing keyword", pr("x", "Related to #4, see #4"), 4, "", false},
		{"keyword inside a word", pr("x", "Prefixes #4"), 4, "", false},
		{"closes the same number in another repo", pr("x", "Fixes org/b#4"), 4, "", false},
		{"closes the same number in another repo by URL", pr("x", "Fixes https://github.com/org/b/issues/4"), 4, "", false},
		{"repo matching is case-insensitive", pr("x", "Fixes Org/A#4"), 4, "", true},

		// Linked issues.
		{"linked issue", pr("x", "", "org/a#4"), 4, "", true},
		{"linked issue with a longer number", pr("x", "", "org/a#42"), 4, "", false},
		{"linked issue in another repo", pr("x", "", "org/b#4"), 4, "", false},

		// Unrelated.
		{"unrelated PR", pr("feat/other-work", "Refactor the logger. Bumps v4 to v5."), 4, "wa/issue-4", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.pr.Covers(repo, tc.issue, tc.branch); got != tc.want {
				t.Errorf("PR{branch %q, body %q, closing %v}.Covers(%s#%d, branch %q) = %v, want %v",
					tc.pr.HeadRefName, tc.pr.Body, tc.pr.ClosingIssues, repo, tc.issue, tc.branch, got, tc.want)
			}
		})
	}
}

func TestPullRequestInAnotherRepoNeverCovers(t *testing.T) {
	pr := PullRequest{Repo: "org/b", Number: 1, HeadRefName: "issue-4", Body: "Fixes org/a#4", ClosingIssues: []string{"org/a#4"}}
	if pr.Covers("org/a", 4, "issue-4") {
		t.Error("a PR in org/b covers org/a#4")
	}
}
