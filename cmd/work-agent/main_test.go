package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
)

const (
	operator      = "dwatts1772"
	operatorToken = "gho_OperatorToken0123456789abcdefABCDEF"
	otherToken    = "gho_OtherAccountToken0123456789abcdefAB"
)

type cli struct {
	stubs      *testharness.Stubs
	configPath string
}

func newCLI(t *testing.T, fx testharness.Fixture) *cli {
	t.Helper()
	stubs := testharness.New(t)
	stubs.SetFixture(t, fx)
	cfg := map[string]any{
		"github": map[string]any{
			"account":          operator,
			"repos":            []string{"org/a", "org/b"},
			"eligibilityLabel": "agent-ready",
		},
		"binaries": stubs.Paths,
	}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return &cli{stubs: stubs, configPath: path}
}

func (c *cli) run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(append(args, "--config", c.configPath), &out, &errOut)
	return code, out.String(), errOut.String()
}

func (c *cli) logFile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(c.configPath), "logs", "work-agent.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// world is gh logged in as both the Operator and another account, with the
// other account active — so anything relying on gh's active account or @me
// would act as the wrong person.
func world() testharness.Fixture {
	return testharness.Fixture{
		Tokens:        map[string]string{operator: operatorToken, "other": otherToken},
		Users:         map[string]string{operatorToken: operator, otherToken: "other"},
		ActiveAccount: "other",
		Issues: map[string][]testharness.Issue{
			"org/a": {
				{Number: 1, Title: "Eligible in a", URL: "https://github.com/org/a/issues/1", Labels: []string{"bug", "agent-ready"}, Assignees: []string{operator}},
				{Number: 2, Title: "Assigned but not labelled", URL: "https://github.com/org/a/issues/2", Labels: []string{"bug"}, Assignees: []string{operator}},
				{Number: 3, Title: "Labelled but someone else's", URL: "https://github.com/org/a/issues/3", Labels: []string{"agent-ready"}, Assignees: []string{"other"}},
			},
			"org/b": {
				{Number: 7, Title: "Eligible in b", URL: "https://github.com/org/b/issues/7", Labels: []string{"agent-ready"}, Assignees: []string{"other", operator}},
			},
			"org/not-allowlisted": {
				{Number: 9, Title: "Outside the allowlist", Labels: []string{"agent-ready"}, Assignees: []string{operator}},
			},
		},
	}
}

func TestTickListsEligibleIssuesInAllowlistedRepos(t *testing.T) {
	c := newCLI(t, world())

	code, stdout, stderr := c.run(t, "tick")

	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"org/a#1", "Eligible in a", "org/b#7", "Eligible in b"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"org/a#2", "org/a#3", "#9"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("stdout lists %s, which is not Eligible:\n%s", unwanted, stdout)
		}
	}
}

func TestTickSaysSoWhenNothingIsEligible(t *testing.T) {
	fx := world()
	fx.Issues = nil
	c := newCLI(t, fx)

	code, stdout, stderr := c.run(t, "tick", "--dry-run")

	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "No Eligible issues") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestEveryGitHubCallActsAsTheOperator(t *testing.T) {
	t.Setenv("GH_TOKEN", otherToken)
	c := newCLI(t, world())

	if code, _, stderr := c.run(t, "tick"); code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}

	calls := c.stubs.Calls(t)
	if len(calls) < 3 {
		t.Fatalf("expected auth, verify and list calls, got %+v", calls)
	}
	first := calls[0]
	if strings.Join(first.Args, " ") != "auth token --user "+operator {
		t.Errorf("first call = %v, want auth token --user %s", first.Args, operator)
	}
	if _, ok := first.Env["GH_TOKEN"]; ok {
		t.Errorf("auth token ran with an inherited GH_TOKEN")
	}
	for _, call := range calls[1:] {
		if call.Bin != "gh" {
			continue
		}
		if call.Env["GH_TOKEN"] != operatorToken {
			t.Errorf("gh %v ran without the Operator's token", call.Args)
		}
		for _, a := range call.Args {
			if a == "@me" {
				t.Errorf("gh %v relies on @me", call.Args)
			}
		}
	}
}

func TestTickRefusesWhenGHIsNotLoggedInAsTheOperator(t *testing.T) {
	fx := world()
	delete(fx.Tokens, operator)
	c := newCLI(t, fx)

	code, stdout, stderr := c.run(t, "tick")

	if code == 0 {
		t.Fatalf("exit 0, want refusal; stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, operator) || !strings.Contains(stderr, "gh auth login") {
		t.Errorf("stderr should name the account and how to fix it:\n%s", stderr)
	}
	assertNoIssueListing(t, c)
}

func TestTickRefusesWhenTheTokenBelongsToSomeoneElse(t *testing.T) {
	fx := world()
	fx.Users[operatorToken] = "impostor"
	c := newCLI(t, fx)

	code, _, stderr := c.run(t, "tick")

	if code == 0 {
		t.Fatal("exit 0, want refusal")
	}
	if !strings.Contains(stderr, "impostor") || !strings.Contains(stderr, operator) {
		t.Errorf("stderr should name both logins:\n%s", stderr)
	}
	assertNoIssueListing(t, c)
}

func assertNoIssueListing(t *testing.T, c *cli) {
	t.Helper()
	for _, call := range c.stubs.Calls(t) {
		if len(call.Args) > 1 && call.Args[0] == "issue" {
			t.Errorf("listed issues despite failed Operator verification: %v", call.Args)
		}
	}
}

func TestTokenNeverAppearsInLogsOrOutput(t *testing.T) {
	cases := map[string]func(*testharness.Fixture){
		"success": func(*testharness.Fixture) {},
		"verification fails and gh echoes the token": func(fx *testharness.Fixture) {
			delete(fx.Users, operatorToken)
			fx.EchoTokenOnError = true
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			fx := world()
			mutate(&fx)
			c := newCLI(t, fx)

			_, stdout, stderr := c.run(t, "tick")

			for sink, text := range map[string]string{"stdout": stdout, "stderr": stderr, "log file": c.logFile(t)} {
				if strings.Contains(text, operatorToken) {
					t.Errorf("%s contains the token:\n%s", sink, text)
				}
			}
		})
	}
}

func TestBadInvocationsFail(t *testing.T) {
	for _, args := range [][]string{{}, {"frobnicate"}, {"tick", "--nope"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code == 0 {
			t.Errorf("run(%v) exited 0", args)
		}
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"tick", "--config", filepath.Join(t.TempDir(), "missing.json")}, &out, &errOut); code == 0 {
		t.Error("tick with missing config exited 0")
	}
}
