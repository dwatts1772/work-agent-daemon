package workspace_test

import (
	"context"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/process"
	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

// newOrca returns the Orca adapter driving a stub orca whose runtime is rt
// (nil: Orca is not running).
func newOrca(t *testing.T, rt *testharness.Orca) (*workspace.Orca, *testharness.Stubs) {
	t.Helper()
	stubs := testharness.New(t)
	stubs.SetFixture(t, testharness.Fixture{Orca: rt})
	runner := process.NewRunner(stubs.Paths, logging.New(io.Discard, io.Discard))
	return workspace.NewOrca(runner), stubs
}

func running() *testharness.Orca {
	return &testharness.Orca{Repos: map[string]string{"repo-a": "org/a", "repo-b": "org/b"}}
}

var ctx = context.Background()

func TestOrcaIsAvailableOnlyWhenItsRuntimeIsReachable(t *testing.T) {
	up, _ := newOrca(t, running())
	down, _ := newOrca(t, nil)

	if ok, err := up.Available(ctx); !ok || err != nil {
		t.Errorf("running Orca: Available() = %v, %v; want true", ok, err)
	}
	if ok, _ := down.Available(ctx); ok {
		t.Errorf("stopped Orca: Available() = true")
	}
}

func TestCreateForIssueMakesOneOrcaWorktreeLinkedToTheIssue(t *testing.T) {
	orca, stubs := newOrca(t, running())

	ws, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/b", Issue: 42})
	if err != nil {
		t.Fatal(err)
	}

	wts := stubs.OrcaWorktrees(t)
	if len(wts) != 1 {
		t.Fatalf("Orca worktrees = %+v, want exactly one", wts)
	}
	wt := wts[0]
	if wt.RepoID != "repo-b" || wt.Issue != 42 || wt.Name != "issue-42" {
		t.Errorf("worktree = %+v, want issue-42 in repo-b linked to issue 42", wt)
	}
	if ws.OrcaIdentityKey != wt.IdentityKey || ws.Path != wt.Path || ws.Branch != "issue-42" {
		t.Errorf("Workspace = %+v, want identity, path and branch of %+v", ws, wt)
	}
	if !uuidPattern.MatchString(ws.ClaudeSessionID) {
		t.Errorf("ClaudeSessionID = %q, want a daemon-generated UUID", ws.ClaudeSessionID)
	}
	for _, c := range stubs.Calls(t) {
		if c.Bin == "orca" && slices.Contains(c.Args, "--agent") {
			t.Errorf("orca %v starts an agent; the daemon starts Claude itself with its own session ID", c.Args)
		}
	}
}

func TestEachWorkspaceGetsItsOwnSessionID(t *testing.T) {
	orca, _ := newOrca(t, running())

	a, errA := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 1})
	b, errB := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 2})

	if errA != nil || errB != nil {
		t.Fatal(errA, errB)
	}
	if a.ClaudeSessionID == b.ClaudeSessionID {
		t.Errorf("two Workspaces share session ID %s", a.ClaudeSessionID)
	}
}

func TestCreateForIssueFailsForARepoOrcaDoesNotKnow(t *testing.T) {
	orca, stubs := newOrca(t, running())

	_, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/unregistered", Issue: 1})

	if err == nil || !strings.Contains(err.Error(), "org/unregistered") {
		t.Errorf("err = %v, want one naming the unregistered repo", err)
	}
	if wts := stubs.OrcaWorktrees(t); len(wts) != 0 {
		t.Errorf("created %+v", wts)
	}
}

func TestCreateForIssueFailsWhenOrcaIsDown(t *testing.T) {
	orca, _ := newOrca(t, nil)

	if _, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 1}); err == nil {
		t.Error("created a Workspace with Orca down")
	}
}

func TestExistsFindsOnlyWorkspacesOrcaStillHas(t *testing.T) {
	orca, _ := newOrca(t, running())
	ws, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 1})
	if err != nil {
		t.Fatal(err)
	}
	gone := ws
	gone.OrcaIdentityKey = "wt2:local:deleted"

	if ok, err := orca.Exists(ctx, ws); !ok || err != nil {
		t.Errorf("Exists(created) = %v, %v; want true", ok, err)
	}
	if ok, err := orca.Exists(ctx, gone); ok || err != nil {
		t.Errorf("Exists(unknown) = %v, %v; want false, nil", ok, err)
	}
}

func TestExistsIsAnErrorWhenOrcaIsDown(t *testing.T) {
	orca, _ := newOrca(t, nil)

	if _, err := orca.Exists(ctx, workspace.Workspace{OrcaIdentityKey: "wt2:local:x"}); err == nil {
		t.Error("Exists with Orca down returned no error; a down Orca must not look like a deleted Workspace")
	}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestAgentStateReadsTheWorkspacesAgentsFromWorktreePs(t *testing.T) {
	cases := []struct {
		agents []string
		want   workspace.AgentState
	}{
		{nil, workspace.AgentNone},
		{[]string{"idle"}, workspace.AgentIdle},
		{[]string{"done"}, workspace.AgentIdle},
		{[]string{"waiting"}, workspace.AgentWaiting},
		{[]string{"blocked"}, workspace.AgentWaiting},
		{[]string{"working"}, workspace.AgentWorking},
		{[]string{"idle", "waiting", "working"}, workspace.AgentWorking},
	}
	for _, tc := range cases {
		rt := running()
		orca, stubs := newOrca(t, rt)
		ws, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 1})
		if err != nil {
			t.Fatal(err)
		}
		other, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 2})
		if err != nil {
			t.Fatal(err)
		}
		rt.AgentStates = map[string][]string{"issue-1": tc.agents, "issue-2": {"working"}}
		stubs.SetFixture(t, testharness.Fixture{Orca: rt})

		got, err := orca.AgentState(ctx, ws)

		if err != nil || got != tc.want {
			t.Errorf("agents %v: AgentState() = %q, %v; want %q", tc.agents, got, err, tc.want)
		}
		if tc.agents == nil {
			if s, _ := orca.AgentState(ctx, other); s != workspace.AgentWorking {
				t.Errorf("another Workspace's agent leaked: %q", s)
			}
		}
	}
}

func TestAgentStateIsNoneForAWorkspaceOrcaDoesNotList(t *testing.T) {
	orca, _ := newOrca(t, running())

	got, err := orca.AgentState(ctx, workspace.Workspace{OrcaIdentityKey: "wt2:local:x", Path: "/nowhere"})

	if err != nil || got != workspace.AgentNone {
		t.Errorf("AgentState() = %q, %v; want none", got, err)
	}
}

func TestWakeStartsClaudeInTheWorkspaceWithItsSessionIDAndThePrompt(t *testing.T) {
	orca, stubs := newOrca(t, running())
	ws, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 7})
	if err != nil {
		t.Fatal(err)
	}

	if err := orca.Wake(ctx, ws, "/work-item issue org/a#7"); err != nil {
		t.Fatal(err)
	}

	calls := stubs.Calls(t)
	last := calls[len(calls)-1]
	want := []string{
		"terminal", "create",
		"--worktree", "identity:" + ws.OrcaIdentityKey,
		"--command", `claude --session-id ` + ws.ClaudeSessionID + ` "/work-item issue org/a#7"`,
		"--json",
	}
	if !slices.Equal(last.Args, want) {
		t.Errorf("orca %q\nwant %q", last.Args, want)
	}
}

func TestWakeRefusesPromptsAShellCouldInterpret(t *testing.T) {
	orca, stubs := newOrca(t, running())
	ws, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 7})
	if err != nil {
		t.Fatal(err)
	}
	before := len(stubs.Calls(t))

	for _, prompt := range []string{`/work-item issue "x"`, "/w $(rm -rf ~)", "/w `id`", "/w a; b", "/w a\nb", ""} {
		if err := orca.Wake(ctx, ws, prompt); err == nil {
			t.Errorf("Wake(%q) succeeded", prompt)
		}
	}
	bad := ws
	bad.ClaudeSessionID = "x; rm -rf ~"
	if err := orca.Wake(ctx, bad, "/work-item issue org/a#7"); err == nil {
		t.Error("Wake with a malformed session ID succeeded")
	}
	if after := len(stubs.Calls(t)); after != before {
		t.Errorf("refused Wakes still ran orca")
	}
}

func TestWakeFailsWhenTheWorkspaceIsGone(t *testing.T) {
	orca, _ := newOrca(t, running())

	err := orca.Wake(ctx, workspace.Workspace{OrcaIdentityKey: "wt2:local:gone", ClaudeSessionID: "6f1c1f51-7d3e-4b8e-9a43-1d2f3c4b5a69"}, "/work-item issue org/a#7")

	if err == nil {
		t.Error("Wake into a missing Workspace succeeded")
	}
}
