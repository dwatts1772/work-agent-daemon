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

	if bin != "gh" && bin != "orca" {
		fmt.Printf("stub %s\n", bin)
		return
	}
	var fx testharness.Fixture
	data, err := os.ReadFile(filepath.Join(dir, testharness.FixtureFile))
	if err == nil {
		err = json.Unmarshal(data, &fx)
	}
	// Without a fixture the stub orca is a reachable runtime.
	if err != nil && !(bin == "orca" && os.IsNotExist(err)) {
		fail(1, "stub %s: fixture: %v", bin, err)
	}
	if bin == "orca" {
		orca(fx, args)
		return
	}
	gh(fx, args)
}

// orca answers `orca status --json` in the shape of Orca 1.4.219.
func orca(fx testharness.Fixture, args []string) {
	if !slices.Equal(args, []string{"status", "--json"}) {
		fmt.Println("stub orca")
		return
	}
	switch fx.Orca {
	case testharness.OrcaFailing:
		fail(1, "orca: internal error")
	case testharness.OrcaUnreachable:
		fmt.Println(`{"id":"local-status","ok":true,"result":{"app":{"running":false},"runtime":{"state":"stopped","reachable":false}}}`)
	default:
		fmt.Println(`{"id":"local-status","ok":true,"result":{"app":{"running":true},"runtime":{"state":"ready","reachable":true,"capabilities":[]}}}`)
	}
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
	fs.String("state", "open", "")
	fs.String("json", "", "")
	fs.Int("limit", 30, "")
	if err := fs.Parse(args); err != nil {
		fail(2, "stub gh: %v", err)
	}
	if *repo == "" {
		fail(2, "stub gh: --repo is required")
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

func fail(code int, format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(code)
}
