// Command stub impersonates gh, git, orca or claude for tests; see package
// testharness.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		fail(1, "stub: %v", err)
	}
	dir := filepath.Dir(exe)
	bin := strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
	args := os.Args[1:]
	record(dir, bin, args)

	data, err := os.ReadFile(filepath.Join(dir, testharness.FixtureFile))
	isRevParse := bin == "git" && slices.Contains(args, "rev-parse")
	if bin != "gh" && !isRevParse && (bin != "orca" || os.IsNotExist(err)) {
		fmt.Printf("stub %s\n", bin)
		return
	}
	var fx testharness.Fixture
	if err == nil {
		err = json.Unmarshal(data, &fx)
	}
	if err != nil {
		fail(1, "stub %s: fixture: %v", bin, err)
	}
	if bin == "orca" {
		orca(dir, fx.Orca, args)
		return
	}
	if isRevParse {
		revParse(fx, args)
		return
	}
	gh(fx, args)
}

func record(dir, bin string, args []string) {
	call := testharness.Call{Bin: bin, Args: args, Env: map[string]string{}}
	for _, k := range testharness.RecordedEnv {
		if v, ok := os.LookupEnv(k); ok {
			call.Env[k] = v
		}
	}
	line, _ := json.Marshal(call)
	f, err := os.OpenFile(filepath.Join(dir, testharness.CallsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fail(1, "stub: record: %v", err)
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

func gh(fx testharness.Fixture, args []string) {
	switch {
	case len(args) >= 2 && args[0] == "auth" && args[1] == "token":
		authToken(fx, args[2:])
	case len(args) >= 2 && args[0] == "api" && args[1] == "user":
		login := currentLogin(fx)
		out, _ := json.Marshal(map[string]any{"login": login, "id": 1})
		fmt.Println(string(out))
	case len(args) >= 2 && args[0] == "issue" && args[1] == "list":
		currentLogin(fx)
		issueList(fx, args[2:])
	case len(args) >= 3 && args[0] == "issue" && args[1] == "view":
		currentLogin(fx)
		issueView(fx, args[2], args[3:])
	case len(args) >= 2 && args[0] == "pr" && args[1] == "list":
		currentLogin(fx)
		prList(fx, args[2:])
	case len(args) >= 3 && args[0] == "pr" && args[1] == "view":
		currentLogin(fx)
		prView(fx, args[2], args[3:])
	default:
		fail(2, "stub gh: unsupported command %q", args)
	}
}

// authToken mirrors real gh: an environment token wins over stored ones.
func authToken(fx testharness.Fixture, args []string) {
	fs := flag.NewFlagSet("auth token", flag.ContinueOnError)
	user := fs.String("user", "", "")
	fs.Parse(args)
	if t := os.Getenv("GH_TOKEN"); t != "" {
		fmt.Println(t)
		return
	}
	account := *user
	if account == "" {
		account = fx.ActiveAccount
	}
	t, ok := fx.Tokens[account]
	if !ok {
		fail(1, "no oauth token found for github.com account %s", account)
	}
	fmt.Println(t)
}

// currentLogin resolves who the request authenticates as, failing like the
// GitHub API does for an unknown token.
func currentLogin(fx testharness.Fixture) string {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = fx.Tokens[fx.ActiveAccount]
	}
	login, ok := fx.Users[token]
	if !ok {
		if fx.EchoTokenOnError {
			fail(1, "HTTP 401: Bad credentials (token %s)", token)
		}
		fail(1, "HTTP 401: Bad credentials")
	}
	return login
}

func issueList(fx testharness.Fixture, args []string) {
	fs := flag.NewFlagSet("issue list", flag.ContinueOnError)
	repo := fs.String("repo", "", "")
	assignee := fs.String("assignee", "", "")
	label := fs.String("label", "", "")
	state := fs.String("state", "open", "")
	fs.String("json", "", "")
	fs.Int("limit", 30, "")
	if err := fs.Parse(args); err != nil {
		fail(2, "stub gh: %v", err)
	}
	if *repo == "" {
		fail(2, "stub gh: --repo is required")
	}
	if *state != "open" {
		fail(2, "stub gh: only --state open is supported")
	}
	if *assignee == "@me" {
		fail(2, "stub gh: @me resolves via the active account and is forbidden")
	}

	type name struct {
		Name string `json:"name"`
	}
	type login struct {
		Login string `json:"login"`
	}
	type issue struct {
		Number    int     `json:"number"`
		Title     string  `json:"title"`
		URL       string  `json:"url"`
		Labels    []name  `json:"labels"`
		Assignees []login `json:"assignees"`
	}
	out := []issue{}
	for _, is := range fx.Issues[*repo] {
		if issueState(is) != "OPEN" {
			continue
		}
		if *assignee != "" && !slices.Contains(is.Assignees, *assignee) {
			continue
		}
		if *label != "" && !slices.Contains(is.Labels, *label) {
			continue
		}
		o := issue{Number: is.Number, Title: is.Title, URL: is.URL}
		for _, l := range is.Labels {
			o.Labels = append(o.Labels, name{l})
		}
		for _, a := range is.Assignees {
			o.Assignees = append(o.Assignees, login{a})
		}
		out = append(out, o)
	}
	data, _ := json.Marshal(out)
	fmt.Println(string(data))
}

func issueState(is testharness.Issue) string {
	if is.State == "" {
		return "OPEN"
	}
	return is.State
}

func issueView(fx testharness.Fixture, number string, args []string) {
	fs := flag.NewFlagSet("issue view", flag.ContinueOnError)
	repo := fs.String("repo", "", "")
	fs.String("json", "", "")
	if err := fs.Parse(args); err != nil {
		fail(2, "stub gh: %v", err)
	}
	for _, is := range fx.Issues[*repo] {
		if fmt.Sprint(is.Number) == number {
			out, _ := json.Marshal(map[string]any{"number": is.Number, "state": issueState(is)})
			fmt.Println(string(out))
			return
		}
	}
	fail(1, "GraphQL: Could not resolve to an issue or pull request with the number of %s.", number)
}

// prList lists the newest PRs first, as gh does.
func prList(fx testharness.Fixture, args []string) {
	fs := flag.NewFlagSet("pr list", flag.ContinueOnError)
	repo := fs.String("repo", "", "")
	author := fs.String("author", "", "")
	state := fs.String("state", "open", "")
	search := fs.String("search", "", "")
	fs.String("json", "", "")
	limit := fs.Int("limit", 30, "")
	if err := fs.Parse(args); err != nil {
		fail(2, "stub gh: %v", err)
	}
	if *repo == "" {
		fail(2, "stub gh: --repo is required")
	}
	if *author == "@me" || strings.Contains(*search, "@me") {
		fail(2, "stub gh: @me resolves via the active account and is forbidden")
	}
	// The only search the daemon makes: PRs requesting a user's review
	// directly, not through a team.
	requested, hasSearch := strings.CutPrefix(*search, "user-review-requested:")
	if *search != "" && !hasSearch {
		fail(2, "stub gh: unsupported --search %q", *search)
	}
	type ref struct {
		Number     int `json:"number"`
		Repository struct {
			Name  string `json:"name"`
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repository"`
	}
	type pr struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		State       string `json:"state"`
		Title       string `json:"title"`
		HeadRefName string `json:"headRefName"`
		HeadRefOid  string `json:"headRefOid"`
		Body        string `json:"body"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
		ClosingIssuesReferences []ref            `json:"closingIssuesReferences"`
		ReviewRequests          []map[string]any `json:"reviewRequests"`
		StatusCheckRollup       []map[string]any `json:"statusCheckRollup"`
	}
	out := []pr{}
	prs := fx.PullRequests[*repo]
	for i := len(prs) - 1; i >= 0 && len(out) < *limit; i-- {
		p := prs[i]
		if *author != "" && p.Author != *author {
			continue
		}
		if *state != "all" && !strings.EqualFold(*state, p.State) {
			continue
		}
		if hasSearch && !slices.Contains(p.ReviewRequests, requested) {
			continue
		}
		o := pr{Number: p.Number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", *repo, p.Number), State: p.State, Title: p.Title, HeadRefName: p.HeadRefName, HeadRefOid: p.HeadSHA, Body: p.Body,
			ClosingIssuesReferences: []ref{}, ReviewRequests: []map[string]any{}, StatusCheckRollup: rollup(p.Checks)}
		for _, r := range p.ReviewRequests {
			if org, team, ok := strings.Cut(r, "/"); ok {
				o.ReviewRequests = append(o.ReviewRequests, map[string]any{"__typename": "Team", "name": team, "slug": org + "/" + team})
			} else {
				o.ReviewRequests = append(o.ReviewRequests, map[string]any{"__typename": "User", "login": r})
			}
		}
		o.Author.Login = p.Author
		for _, c := range p.ClosingIssues {
			var r ref
			repoName, num, _ := strings.Cut(c, "#")
			owner, name, _ := strings.Cut(repoName, "/")
			fmt.Sscan(num, &r.Number)
			r.Repository.Name, r.Repository.Owner.Login = name, owner
			o.ClosingIssuesReferences = append(o.ClosingIssuesReferences, r)
		}
		out = append(out, o)
	}
	data, _ := json.Marshal(out)
	fmt.Println(string(data))
}

// prView reports a PR's state, head commit, the checks on it, and its
// reviews and comments.
func prView(fx testharness.Fixture, number string, args []string) {
	fs := flag.NewFlagSet("pr view", flag.ContinueOnError)
	repo := fs.String("repo", "", "")
	fs.String("json", "", "")
	if err := fs.Parse(args); err != nil {
		fail(2, "stub gh: %v", err)
	}
	for _, p := range fx.PullRequests[*repo] {
		if fmt.Sprint(p.Number) != number {
			continue
		}
		reviews := []map[string]any{}
		for _, r := range p.Reviews {
			reviews = append(reviews, map[string]any{"id": r.ID, "author": map[string]any{"login": r.Author}, "authorAssociation": r.AuthorAssociation, "body": r.Body, "state": r.State, "submittedAt": r.SubmittedAt})
		}
		comments := []map[string]any{}
		for _, c := range p.Comments {
			comments = append(comments, map[string]any{"id": c.ID, "author": map[string]any{"login": c.Author}, "authorAssociation": c.AuthorAssociation, "body": c.Body, "createdAt": c.CreatedAt})
		}
		out, _ := json.Marshal(map[string]any{"state": p.State, "headRefOid": p.HeadSHA, "statusCheckRollup": rollup(p.Checks), "reviews": reviews, "comments": comments})
		fmt.Println(string(out))
		return
	}
	fail(1, "GraphQL: Could not resolve to a PullRequest with the number of %s.", number)
}

// revParse resolves the refs/remotes/origin/pr/<n> a fetch of the pull ref
// made in an Orca clone (`git -C <clones/<repo id>> rev-parse <ref>`): the
// fixture's PullRefs entry, else the PR's head.
func revParse(fx testharness.Fixture, args []string) {
	ref := args[len(args)-1]
	n, isPullRef := strings.CutPrefix(ref, "refs/remotes/origin/pr/")
	repo := ""
	if fx.Orca != nil && len(args) > 1 && args[0] == "-C" {
		repo = fx.Orca.Repos[filepath.Base(args[1])]
	}
	if sha, ok := fx.PullRefs[repo+"#"+n]; isPullRef && ok {
		fmt.Println(sha)
		return
	}
	for _, p := range fx.PullRequests[repo] {
		if isPullRef && fmt.Sprint(p.Number) == n && p.HeadSHA != "" {
			fmt.Println(p.HeadSHA)
			return
		}
	}
	fail(128, "fatal: ambiguous argument '%s': unknown revision or path not in the working tree.", ref)
}

// rollup is checks as statusCheckRollup reports them.
func rollup(checks []testharness.Check) []map[string]any {
	out := []map[string]any{}
	for _, c := range checks {
		out = append(out, map[string]any{"__typename": "CheckRun", "name": c.Name, "status": c.Status, "conclusion": c.Conclusion})
	}
	return out
}

func fail(code int, format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(code)
}
