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
	if bin != "gh" && (bin != "orca" || os.IsNotExist(err)) {
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
	fs.String("json", "", "")
	limit := fs.Int("limit", 30, "")
	if err := fs.Parse(args); err != nil {
		fail(2, "stub gh: %v", err)
	}
	if *repo == "" {
		fail(2, "stub gh: --repo is required")
	}
	if *author == "@me" {
		fail(2, "stub gh: @me resolves via the active account and is forbidden")
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
		HeadRefName string `json:"headRefName"`
		Body        string `json:"body"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
		ClosingIssuesReferences []ref `json:"closingIssuesReferences"`
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
		o := pr{Number: p.Number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", *repo, p.Number), State: p.State, HeadRefName: p.HeadRefName, Body: p.Body, ClosingIssuesReferences: []ref{}}
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

func fail(code int, format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(code)
}
