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
