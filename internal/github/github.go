// Package github talks to GitHub through the gh CLI, always as the Operator.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/process"
)

// Client runs gh as the Operator: every call carries the Operator's token in
// GH_TOKEN, so gh's active account and "@me" never come into play.
type Client struct {
	runner  *process.Runner
	account string
	env     []string
}

// Connect fetches the Operator's token from gh's credential store and
// verifies that GitHub identifies it as account. It fails, naming the
// account, when gh cannot act as the Operator.
func Connect(ctx context.Context, runner *process.Runner, log *logging.Logger, account string) (*Client, error) {
	out, err := runner.Run(ctx, "gh", []string{"auth", "token", "--user", account})
	token := strings.TrimSpace(string(out))
	log.AddSecret(token)
	if err != nil || token == "" {
		return nil, fmt.Errorf("gh cannot act as Operator %q: no token for that account (run `gh auth login` for %s): %v", account, account, err)
	}

	c := &Client{runner: runner, account: account, env: []string{"GH_TOKEN=" + token}}
	out, err = c.gh(ctx, "api", "user")
	if err != nil {
		return nil, fmt.Errorf("gh cannot act as Operator %q: verifying its token failed: %w", account, err)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(out, &user); err != nil {
		return nil, fmt.Errorf("gh cannot act as Operator %q: unreadable `gh api user` response: %w", account, err)
	}
	if !strings.EqualFold(user.Login, account) {
		return nil, fmt.Errorf("gh cannot act as Operator %q: its token authenticates as %q", account, user.Login)
	}
	return c, nil
}

func (c *Client) gh(ctx context.Context, args ...string) ([]byte, error) {
	return c.runner.Run(ctx, "gh", args, c.env...)
}

// Issue is an open GitHub issue.
type Issue struct {
	Repo   string
	Number int
	Title  string
	URL    string
	Labels []string
}

// Ref is the issue's "owner/name#number" reference.
func (i Issue) Ref() string { return i.Repo + "#" + strconv.Itoa(i.Number) }

// issueLimit caps one listing; far above what one Operator is assigned.
const issueLimit = 200

// EligibleIssues lists the open issues in repo that are assigned to the
// Operator and carry label.
func (c *Client) EligibleIssues(ctx context.Context, repo, label string) ([]Issue, error) {
	out, err := c.gh(ctx, "issue", "list",
		"--repo", repo,
		"--assignee", c.account,
		"--label", label,
		"--state", "open",
		"--limit", strconv.Itoa(issueLimit),
		"--json", "number,title,url,labels,assignees",
	)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Assignees []struct {
			Login string `json:"login"`
		} `json:"assignees"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse issues for %s: %w", repo, err)
	}

	// gh filters server-side; re-check so a filtering change can never make
	// the daemon work on an issue that is not Eligible.
	var issues []Issue
	for _, r := range raw {
		issue := Issue{Repo: repo, Number: r.Number, Title: r.Title, URL: r.URL}
		hasLabel, assigned := false, false
		for _, l := range r.Labels {
			issue.Labels = append(issue.Labels, l.Name)
			hasLabel = hasLabel || strings.EqualFold(l.Name, label)
		}
		for _, a := range r.Assignees {
			assigned = assigned || strings.EqualFold(a.Login, c.account)
		}
		if hasLabel && assigned {
			issues = append(issues, issue)
		}
	}
	return issues, nil
}

// prLimit caps one listing of the Operator's pull requests in a repo, newest
// first; far above what the Operator opens between two Ticks.
const prLimit = 100

// OperatorPullRequests lists the pull requests in repo opened by the
// Operator, in any state, newest first.
func (c *Client) OperatorPullRequests(ctx context.Context, repo string) ([]PullRequest, error) {
	out, err := c.gh(ctx, "pr", "list",
		"--repo", repo,
		"--author", c.account,
		"--state", "all",
		"--limit", strconv.Itoa(prLimit),
		"--json", "number,url,state,headRefName,body,author,closingIssuesReferences",
	)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number      int     `json:"number"`
		URL         string  `json:"url"`
		State       PRState `json:"state"`
		HeadRefName string  `json:"headRefName"`
		Body        string  `json:"body"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
		ClosingIssuesReferences []struct {
			Number     int `json:"number"`
			Repository struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repository"`
		} `json:"closingIssuesReferences"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse pull requests for %s: %w", repo, err)
	}

	// Re-check the author, as EligibleIssues re-checks its filters.
	var prs []PullRequest
	for _, r := range raw {
		if !strings.EqualFold(r.Author.Login, c.account) {
			continue
		}
		pr := PullRequest{Repo: repo, Number: r.Number, URL: r.URL, State: r.State, HeadRefName: r.HeadRefName, Body: r.Body, Author: r.Author.Login}
		for _, ref := range r.ClosingIssuesReferences {
			pr.ClosingIssues = append(pr.ClosingIssues, ref.Repository.Owner.Login+"/"+ref.Repository.Name+"#"+strconv.Itoa(ref.Number))
		}
		prs = append(prs, pr)
	}
	return prs, nil
}

// IssueClosed reports whether issue number in repo is closed.
func (c *Client) IssueClosed(ctx context.Context, repo string, number int) (bool, error) {
	out, err := c.gh(ctx, "issue", "view", strconv.Itoa(number), "--repo", repo, "--json", "state")
	if err != nil {
		return false, err
	}
	var issue struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &issue); err != nil {
		return false, fmt.Errorf("parse issue %s#%d: %w", repo, number, err)
	}
	return strings.EqualFold(issue.State, "CLOSED"), nil
}

// CI is the CI state of a pull request's head commit.
type CI struct {
	HeadSHA string
	// Settled is set once every check on the head has concluded; a head
	// with no checks yet is not Settled.
	Settled bool
	// Failed is set when any concluded check failed.
	Failed bool
}

// PullRequestCI reads the head commit of PR number in repo and the state of
// the checks on it, in one call so the two always match.
func (c *Client) PullRequestCI(ctx context.Context, repo string, number int) (CI, error) {
	out, err := c.gh(ctx, "pr", "view", strconv.Itoa(number), "--repo", repo, "--json", "headRefOid,statusCheckRollup")
	if err != nil {
		return CI{}, err
	}
	var pr struct {
		HeadRefOid        string  `json:"headRefOid"`
		StatusCheckRollup []Check `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &pr); err != nil {
		return CI{}, fmt.Errorf("parse pull request %s#%d: %w", repo, number, err)
	}
	settled, failed := Settle(pr.StatusCheckRollup)
	return CI{HeadSHA: pr.HeadRefOid, Settled: settled, Failed: failed}, nil
}

// Check is one entry of a commit's status check rollup: a check run
// (status and conclusion) or a commit status (state).
type Check struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// Settle decides whether checks are Settled — every check run completed and
// no commit status pending, with at least one check — and whether any
// concluded check failed.
func Settle(checks []Check) (settled, failed bool) {
	settled = len(checks) > 0
	for _, c := range checks {
		switch {
		case c.Status != "": // a check run
			if !strings.EqualFold(c.Status, "COMPLETED") {
				settled = false
				continue
			}
			switch strings.ToUpper(c.Conclusion) {
			case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
				failed = true
			}
		default: // a commit status
			switch strings.ToUpper(c.State) {
			case "FAILURE", "ERROR":
				failed = true
			case "SUCCESS":
			default:
				settled = false
			}
		}
	}
	return settled, failed
}
