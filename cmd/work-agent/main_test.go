package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
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

func (c *cli) stateDir() string { return filepath.Dir(c.configPath) }

func (c *cli) savedState(t *testing.T) workflow.State {
	t.Helper()
	st, err := state.Read(c.stateDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (c *cli) mustTick(t *testing.T, args ...string) string {
	t.Helper()
	code, stdout, stderr := c.run(t, append([]string{"tick"}, args...)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	return stdout
}

func itemIDs(st workflow.State) []string {
	var ids []string
	for _, w := range st.Items {
		ids = append(ids, w.ID)
	}
	return ids
}

func TestTickRecordsEachEligibleIssueAsOneOwnedIssue(t *testing.T) {
	c := newCLI(t, world())

	stdout := c.mustTick(t)

	st := c.savedState(t)
	if got := itemIDs(st); !slices.Equal(got, []string{"org/a#1", "org/b#7"}) {
		t.Fatalf("Owned Issues = %v, want org/a#1 and org/b#7", got)
	}
	for _, w := range st.Items {
		if w.Kind != workflow.KindOwnedIssue || w.State != workflow.PendingWorkspace {
			t.Errorf("%s is %s/%s, want OWNED_ISSUE/PENDING_WORKSPACE", w.ID, w.Kind, w.State)
		}
		if !slices.Contains(w.ProcessedEventIDs, w.ID+":assigned") {
			t.Errorf("%s has no dedupe marker: %v", w.ID, w.ProcessedEventIDs)
		}
	}
	if !strings.Contains(stdout, "CREATE_OWNED_ISSUE") {
		t.Errorf("stdout does not report the actions:\n%s", stdout)
	}
}

// Each run() reloads state.json from disk, so a second Tick is also a
// process restart.
func TestReRunningTheTickCreatesNoDuplicatesAndKeepsState(t *testing.T) {
	c := newCLI(t, world())
	c.mustTick(t)
	first := c.savedState(t)

	stdout := c.mustTick(t)

	if again := c.savedState(t); !reflect.DeepEqual(again, first) {
		t.Errorf("re-running the Tick changed state:\nbefore %+v\nafter  %+v", first, again)
	}
	if strings.Contains(stdout, "CREATE_OWNED_ISSUE") {
		t.Errorf("re-run reported new Owned Issues:\n%s", stdout)
	}
}

func TestTickAddsOnlyNewlyEligibleIssues(t *testing.T) {
	c := newCLI(t, world())
	c.mustTick(t)
	before := c.savedState(t)

	fx := world()
	fx.Issues["org/a"][1].Labels = append(fx.Issues["org/a"][1].Labels, "agent-ready")
	c.stubs.SetFixture(t, fx)
	c.mustTick(t)

	after := c.savedState(t)
	if got := itemIDs(after); !slices.Equal(got, []string{"org/a#1", "org/b#7", "org/a#2"}) {
		t.Fatalf("Owned Issues = %v", got)
	}
	if !reflect.DeepEqual(after.Items[:2], before.Items) {
		t.Errorf("existing Owned Issues changed")
	}
}

func TestDryRunPrintsActionsAndWritesNothing(t *testing.T) {
	c := newCLI(t, world())
	c.mustTick(t)
	fx := world()
	fx.Issues["org/a"][1].Labels = append(fx.Issues["org/a"][1].Labels, "agent-ready")
	c.stubs.SetFixture(t, fx)
	before := snapshot(t, c.stateDir())

	stdout := c.mustTick(t, "--dry-run")

	if after := snapshot(t, c.stateDir()); !reflect.DeepEqual(after, before) {
		t.Errorf("--dry-run wrote to the state directory:\nbefore %v\nafter  %v", before, after)
	}
	if !strings.Contains(stdout, "CREATE_OWNED_ISSUE\torg/a#2") {
		t.Errorf("dry run does not print the new Owned Issue:\n%s", stdout)
	}
	if strings.Contains(stdout, "org/a#1") {
		t.Errorf("dry run reports an already-tracked issue:\n%s", stdout)
	}
	for _, call := range c.stubs.Calls(t) {
		if call.Bin == "orca" || call.Bin == "claude" || call.Bin == "git" {
			t.Errorf("--dry-run ran %s %v", call.Bin, call.Args)
		}
	}
}

func TestDryRunOnAFreshStateDirectoryCreatesNothing(t *testing.T) {
	c := newCLI(t, world())
	before := snapshot(t, c.stateDir())

	stdout := c.mustTick(t, "--dry-run")

	if after := snapshot(t, c.stateDir()); !reflect.DeepEqual(after, before) {
		t.Errorf("--dry-run wrote to the state directory:\nbefore %v\nafter  %v", before, after)
	}
	if !strings.Contains(stdout, "CREATE_OWNED_ISSUE\torg/a#1") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestSecondConcurrentTickFailsFastOnTheLock(t *testing.T) {
	c := newCLI(t, world())
	held, err := state.Open(c.stateDir())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	start := time.Now()
	code, _, stderr := c.run(t, "tick")

	if code == 0 {
		t.Fatal("a second Tick ran while the state directory was locked")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %v; must fail fast", took)
	}
	if !strings.Contains(stderr, "locked") {
		t.Errorf("stderr should say the state directory is locked:\n%s", stderr)
	}
	if calls := c.stubs.Calls(t); len(calls) != 0 {
		t.Errorf("touched GitHub despite the lock: %+v", calls)
	}
	if _, err := os.Stat(filepath.Join(c.stateDir(), "state.json")); !os.IsNotExist(err) {
		t.Errorf("state.json written despite the lock")
	}
}

// snapshot maps every file under dir to its contents.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			files[rel+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(path)
		files[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func (c *cli) item(t *testing.T, id string) workflow.WorkItem {
	t.Helper()
	for _, w := range c.savedState(t).Items {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("%s is not tracked", id)
	return workflow.WorkItem{}
}

func TestRemovingTheEligibilityLabelPausesAndReAddingResumes(t *testing.T) {
	c := newCLI(t, world())
	c.mustTick(t)

	fx := world()
	fx.Issues["org/a"][0].Labels = []string{"bug"}
	c.stubs.SetFixture(t, fx)
	stdout := c.mustTick(t)

	if got := c.item(t, "org/a#1").State; got != workflow.Paused {
		t.Fatalf("after removing the label, org/a#1 is %s, want PAUSED", got)
	}
	if got := c.item(t, "org/b#7").State; got != workflow.PendingWorkspace {
		t.Errorf("org/b#7 is %s; only the item that lost Eligibility should pause", got)
	}
	if !strings.Contains(stdout, "PAUSE\torg/a#1") {
		t.Errorf("stdout does not report the pause:\n%s", stdout)
	}

	c.stubs.SetFixture(t, world())
	stdout = c.mustTick(t)

	if got := c.item(t, "org/a#1").State; got != workflow.PendingWorkspace {
		t.Fatalf("after re-adding the label, org/a#1 is %s, want PENDING_WORKSPACE", got)
	}
	if !strings.Contains(stdout, "RESUME\torg/a#1") {
		t.Errorf("stdout does not report the resume:\n%s", stdout)
	}
	if got := itemIDs(c.savedState(t)); !slices.Equal(got, []string{"org/a#1", "org/b#7"}) {
		t.Errorf("Owned Issues = %v; pausing must delete or duplicate nothing", got)
	}
}

func TestReassigningAwayFromTheOperatorPauses(t *testing.T) {
	fx := world()
	fx.Issues = map[string][]testharness.Issue{"org/a": fx.Issues["org/a"][:1]}
	c := newCLI(t, fx)
	c.mustTick(t)

	fx.Issues["org/a"][0].Assignees = []string{"other"}
	c.stubs.SetFixture(t, fx)
	stdout := c.mustTick(t)

	if got := c.item(t, "org/a#1").State; got != workflow.Paused {
		t.Fatalf("after reassigning away, org/a#1 is %s, want PAUSED", got)
	}
	if !strings.Contains(stdout, "PAUSE\torg/a#1") {
		t.Errorf("stdout does not report the pause:\n%s", stdout)
	}

	fx.Issues["org/a"][0].Assignees = []string{operator}
	c.stubs.SetFixture(t, fx)
	c.mustTick(t)

	if got := c.item(t, "org/a#1").State; got != workflow.PendingWorkspace {
		t.Errorf("after reassigning back, org/a#1 is %s, want PENDING_WORKSPACE", got)
	}
}

func (c *cli) must(t *testing.T, args ...string) string {
	t.Helper()
	code, stdout, stderr := c.run(t, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d, stderr:\n%s", args, code, stderr)
	}
	return stdout
}

func TestOperatorPauseHoldsAcrossTicksUntilResumed(t *testing.T) {
	c := newCLI(t, world())
	c.mustTick(t)
	callsBefore := len(c.stubs.Calls(t))

	c.must(t, "pause", "org/a#1")

	if got := len(c.stubs.Calls(t)); got != callsBefore {
		t.Errorf("pause ran %d external commands; it must be local", got-callsBefore)
	}
	if got := c.item(t, "org/a#1").State; got != workflow.Paused {
		t.Fatalf("after pause, org/a#1 is %s, want PAUSED", got)
	}

	c.mustTick(t)
	if got := c.item(t, "org/a#1").State; got != workflow.Paused {
		t.Fatalf("a Tick resumed an Operator-paused item: %s", got)
	}

	c.must(t, "resume", "org/a#1")
	if got := c.item(t, "org/a#1").State; got != workflow.PendingWorkspace {
		t.Errorf("after resume, org/a#1 is %s, want PENDING_WORKSPACE", got)
	}
}

func TestResumeLeavesAnItemThatIsNotEligiblePaused(t *testing.T) {
	c := newCLI(t, world())
	c.mustTick(t)
	c.must(t, "pause", "org/a#1")
	fx := world()
	fx.Issues["org/a"][0].Labels = nil
	c.stubs.SetFixture(t, fx)
	c.mustTick(t)

	// Re-adding the label alone does not undo the Operator's pause.
	c.stubs.SetFixture(t, world())
	c.mustTick(t)
	if got := c.item(t, "org/a#1").State; got != workflow.Paused {
		t.Fatalf("regaining Eligibility undid the Operator's pause: %s", got)
	}

	c.stubs.SetFixture(t, fx)
	c.mustTick(t)
	code, _, stderr := c.run(t, "resume", "org/a#1")

	if code == 0 {
		t.Error("resume of an item that is not Eligible exited 0")
	}
	if !strings.Contains(stderr, "not Eligible") {
		t.Errorf("stderr should say why it is still Paused:\n%s", stderr)
	}
	if got := c.item(t, "org/a#1").State; got != workflow.Paused {
		t.Errorf("org/a#1 is %s; an item that is not Eligible must stay PAUSED", got)
	}

	c.stubs.SetFixture(t, world())
	c.mustTick(t)
	if got := c.item(t, "org/a#1").State; got != workflow.PendingWorkspace {
		t.Errorf("once Eligible again after resume, org/a#1 is %s, want PENDING_WORKSPACE", got)
	}
}

func TestPauseAndResumeRejectUnknownOrMalformedRefs(t *testing.T) {
	c := newCLI(t, world())
	c.mustTick(t)
	for _, args := range [][]string{
		{"pause", "org/a#99"}, {"resume", "org/a#99"}, {"pause", "org/a"}, {"pause"}, {"inspect", "org/a#99"},
	} {
		if code, _, _ := c.run(t, args...); code == 0 {
			t.Errorf("%v exited 0", args)
		}
	}
}

func TestListAndInspectShowPausedItemsAndWhy(t *testing.T) {
	c := newCLI(t, world())
	if out := c.must(t, "list"); !strings.Contains(out, "No Work Items") {
		t.Errorf("list before any Tick:\n%s", out)
	}
	c.mustTick(t)
	c.must(t, "pause", "org/b#7")
	fx := world()
	fx.Issues["org/a"][0].Labels = nil
	c.stubs.SetFixture(t, fx)
	c.mustTick(t)
	callsBefore := len(c.stubs.Calls(t))

	list := c.must(t, "list")

	for _, want := range []string{
		"org/a#1\tPAUSED\tnot Eligible\tEligible in a",
		"org/b#7\tPAUSED\tpaused by Operator\tEligible in b",
	} {
		if !strings.Contains(list, want) {
			t.Errorf("list missing %q:\n%s", want, list)
		}
	}

	out := c.must(t, "inspect", "org/a#1")
	var w workflow.WorkItem
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("inspect output is not a Work Item: %v\n%s", err, out)
	}
	if w.ID != "org/a#1" || w.State != workflow.Paused || w.Pause == nil || !w.Pause.NotEligible || w.Pause.ResumeTo != workflow.PendingWorkspace {
		t.Errorf("inspect = %+v, want org/a#1 PAUSED, not Eligible, resuming to PENDING_WORKSPACE", w)
	}
	if got := len(c.stubs.Calls(t)); got != callsBefore {
		t.Errorf("list/inspect ran %d external commands; they must be local", got-callsBefore)
	}
}

func TestPausedItemsTickWithoutTouchingOrcaClaudeOrGit(t *testing.T) {
	c := newCLI(t, world())
	c.mustTick(t)
	c.must(t, "pause", "org/a#1")
	fx := world()
	fx.Issues["org/b"] = nil
	c.stubs.SetFixture(t, fx)

	c.mustTick(t)
	c.mustTick(t)

	for _, call := range c.stubs.Calls(t) {
		if call.Bin != "gh" {
			t.Errorf("a Tick with only Paused items ran %s %v", call.Bin, call.Args)
		}
	}
}
