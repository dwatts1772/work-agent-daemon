package github

import (
	"regexp"
	"strconv"
	"strings"
)

// PRState is a pull request's state as GitHub reports it.
type PRState string

const (
	PROpen   PRState = "OPEN"
	PRMerged PRState = "MERGED"
	PRClosed PRState = "CLOSED"
)

// PullRequest is a pull request in an allowlisted repo.
type PullRequest struct {
	Repo        string
	Number      int
	URL         string
	State       PRState
	HeadRefName string
	Body        string
	Author      string
	// ClosingIssues are the "owner/name#number" references of the issues
	// GitHub links to the PR as ones it closes, including manual links.
	ClosingIssues []string
}

var (
	// issueBranch finds an issue number in a branch name after "issue-",
	// "issue_", "issue/" or "issues/". The number must end the branch or be
	// followed by a separator, so issue-42 never reads as issue 4. A bare
	// leading number is not enough: "2024-cleanup" is not issue 2024.
	issueBranch = regexp.MustCompile(`(?i)(?:^|[/_-])issues?[-_/]?([0-9]+)(?:$|[/_-])`)
	// closingRef finds a GitHub closing keyword and the issue it closes:
	// "#n", "owner/name#n" or the issue's URL. \b keeps "Prefixes #4" and
	// "#42" from reading as "fixes #4".
	closingRef = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b:?\s+(?:https://github\.com/([\w.-]+/[\w.-]+)/issues/|([\w.-]+/[\w.-]+)?#)([0-9]+)\b`)
)

// Covers reports whether the pull request is for issue in repo: it is in
// repo and either comes from branch (the Workspace's branch, if known), has
// the issue number in its branch name, closes the issue with a closing
// keyword in its body, or is linked to the issue on GitHub.
func (pr PullRequest) Covers(repo string, issue int, branch string) bool {
	if !strings.EqualFold(pr.Repo, repo) {
		return false
	}
	if branch != "" && pr.HeadRefName == branch {
		return true
	}
	n := strconv.Itoa(issue)
	for _, m := range issueBranch.FindAllStringSubmatch(pr.HeadRefName, -1) {
		if m[1] == n {
			return true
		}
	}
	for _, m := range closingRef.FindAllStringSubmatch(pr.Body, -1) {
		target := pr.Repo
		if m[1] != "" {
			target = m[1]
		} else if m[2] != "" {
			target = m[2]
		}
		if m[3] == n && strings.EqualFold(target, repo) {
			return true
		}
	}
	for _, ref := range pr.ClosingIssues {
		if strings.EqualFold(ref, repo+"#"+n) {
			return true
		}
	}
	return false
}
