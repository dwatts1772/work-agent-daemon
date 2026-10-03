// Package testharness provides stub gh, git, orca and claude executables on
// disk, so tests drive the real process runner without touching the network.
//
// All four stubs are copies of one program (./stub) that picks its behaviour
// from its own file name. Each copy reads fixture.json from its directory and
// appends every invocation to calls.jsonl there.
package testharness

import "time"

const (
	FixtureFile = "fixture.json"
	CallsFile   = "calls.jsonl"
)

// Fixture is the GitHub world the stub gh simulates.
type Fixture struct {
	// Tokens maps an account to the token `gh auth token --user <account>`
	// prints. Accounts missing here are not logged in.
	Tokens map[string]string `json:"tokens"`
	// Users maps a token to the login `gh api user` reports for it.
	Users map[string]string `json:"users"`
	// ActiveAccount is gh's active account, used when no GH_TOKEN is set.
	// The daemon must never depend on it.
	ActiveAccount string `json:"activeAccount"`
	// Issues lists issues per "owner/name" repo.
	Issues map[string][]Issue `json:"issues"`
	// PullRequests lists pull requests per "owner/name" repo, oldest first.
	PullRequests map[string][]PullRequest `json:"pullRequests,omitempty"`
	// EchoTokenOnError makes failing gh calls print GH_TOKEN to stderr, as
	// a hostile stand-in for any tool that leaks its credentials in errors.
	EchoTokenOnError bool `json:"echoTokenOnError"`
	// PullRefs maps "owner/name#n" to the commit refs/pull/<n>/head is at
	// when the stub git fetches it; by default the PR's HeadSHA.
	PullRefs map[string]string `json:"pullRefs,omitempty"`
	// Orca is the Orca runtime the stub orca simulates; nil means Orca is
	// not running.
	Orca *Orca `json:"orca,omitempty"`
}

// Orca is a running Orca runtime. Worktrees the stub creates persist in
// OrcaWorktreesFile across fixture changes, as they do across Orca restarts.
type Orca struct {
	// Repos maps each registered repo's Orca id to its GitHub "owner/name".
	Repos map[string]string `json:"repos"`
	// AgentStates maps a worktree name to the states of its agents, as
	// `worktree ps` reports them.
	AgentStates map[string][]string `json:"agentStates,omitempty"`
	// Terminals maps a worktree name to its live terminals, as `terminal
	// list` reports them.
	Terminals map[string][]OrcaTerminal `json:"terminals,omitempty"`
}

// OrcaTerminal is a live terminal in a worktree.
type OrcaTerminal struct {
	Handle string `json:"handle"`
	// AgentIdentity is "claude" while Claude runs in the terminal.
	AgentIdentity string `json:"agentIdentity,omitempty"`
}

// OrcaWorktreesFile holds the worktrees the stub orca has created.
const OrcaWorktreesFile = "orca-worktrees.json"

// OrcaWorktree is one worktree the stub orca created.
type OrcaWorktree struct {
	IdentityKey string `json:"identityKey"`
	RepoID      string `json:"repoId"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	Issue       int    `json:"issue"`
	// BaseBranch is the --base-branch it was created from, if any.
	BaseBranch string `json:"baseBranch,omitempty"`
}

type Issue struct {
	Number int `json:"number"`
	// State is "OPEN" (the default when empty) or "CLOSED".
	State     string   `json:"state,omitempty"`
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Labels    []string `json:"labels"`
	Assignees []string `json:"assignees"`
}

// PullRequest is a pull request the stub gh reports.
type PullRequest struct {
	Number int `json:"number"`
	// State is "OPEN", "MERGED" or "CLOSED".
	State       string `json:"state"`
	Title       string `json:"title,omitempty"`
	HeadRefName string `json:"headRefName"`
	Body        string `json:"body"`
	Author      string `json:"author"`
	// ClosingIssues are the "owner/name#number" issues GitHub links to the
	// PR as ones it closes.
	ClosingIssues []string `json:"closingIssues,omitempty"`
	// ReviewRequests are the PR's requested reviewers: user logins, or
	// "org/team" for a team.
	ReviewRequests []string `json:"reviewRequests,omitempty"`
	// HeadSHA is the PR's head commit and Checks the checks on it.
	HeadSHA string  `json:"headSha,omitempty"`
	Checks  []Check `json:"checks,omitempty"`
	// Reviews and Comments are the PR's reviews and standalone comments,
	// oldest first.
	Reviews  []Review  `json:"reviews,omitempty"`
	Comments []Comment `json:"comments,omitempty"`
}

// Review is a review on a PR, as `gh pr view --json reviews` reports it.
type Review struct {
	ID                string    `json:"id"`
	Author            string    `json:"author"`
	AuthorAssociation string    `json:"authorAssociation"`
	Body              string    `json:"body"`
	State             string    `json:"state"`
	SubmittedAt       time.Time `json:"submittedAt"`
}

// Comment is a standalone comment on a PR, as `gh pr view --json comments`
// reports it.
type Comment struct {
	ID                string    `json:"id"`
	Author            string    `json:"author"`
	AuthorAssociation string    `json:"authorAssociation"`
	Body              string    `json:"body"`
	CreatedAt         time.Time `json:"createdAt"`
}

// Check is a check run on a PR's head commit, as statusCheckRollup reports
// it.
type Check struct {
	Name string `json:"name"`
	// Status is "QUEUED", "IN_PROGRESS" or "COMPLETED".
	Status string `json:"status"`
	// Conclusion is set once COMPLETED: "SUCCESS", "FAILURE", "SKIPPED", ….
	Conclusion string `json:"conclusion,omitempty"`
}

// Call is one recorded stub invocation.
type Call struct {
	Bin  string            `json:"bin"`
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
}

// RecordedEnv lists the environment variables a Call captures.
var RecordedEnv = []string{"GH_TOKEN", "GITHUB_TOKEN", "GIT_TERMINAL_PROMPT"}
