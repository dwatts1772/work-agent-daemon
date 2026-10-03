package workspace_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	return workspace.NewOrca(runner, claudeDir(stubs)), stubs
}

// claudeDir is the Claude config directory of the test's Claude.
func claudeDir(stubs *testharness.Stubs) string { return filepath.Join(stubs.Dir, "claude-config") }

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

// haveTranscript makes Claude's transcript of ws's session exist, as it does
// once Claude has run with that session ID.
func haveTranscript(t *testing.T, stubs *testharness.Stubs, ws workspace.Workspace) {
	t.Helper()
	dir := filepath.Join(claudeDir(stubs), "projects", "C--some-project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ws.ClaudeSessionID+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func lastOrcaCall(t *testing.T, stubs *testharness.Stubs) []string {
	t.Helper()
	calls := stubs.Calls(t)
	return calls[len(calls)-1].Args
}

func TestALaterWakeResumesTheWorkspacesClaudeConversationInANewTerminal(t *testing.T) {
	orca, stubs := newOrca(t, running())
	ws, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 7})
	if err != nil {
		t.Fatal(err)
	}
	haveTranscript(t, stubs, ws)

	if err := orca.Wake(ctx, ws, "/work-item ci-failure org/a#60"); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"terminal", "create",
		"--worktree", "identity:" + ws.OrcaIdentityKey,
		"--command", `claude --resume ` + ws.ClaudeSessionID + ` "/work-item ci-failure org/a#60"`,
		"--json",
	}
	if got := lastOrcaCall(t, stubs); !slices.Equal(got, want) {
		t.Errorf("orca %q\nwant %q", got, want)
	}
}

func TestAWakeIsSentIntoALiveIdleClaude(t *testing.T) {
	rt := running()
	orca, stubs := newOrca(t, rt)
	ws, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 7})
	if err != nil {
		t.Fatal(err)
	}
	haveTranscript(t, stubs, ws)
	rt.AgentStates = map[string][]string{"issue-7": {"idle"}}
	rt.Terminals = map[string][]testharness.OrcaTerminal{"issue-7": {{Handle: "term_shell"}, {Handle: "term_claude", AgentIdentity: "claude"}}}
	stubs.SetFixture(t, testharness.Fixture{Orca: rt})

	if err := orca.Wake(ctx, ws, "/work-item ci-failure org/a#60"); err != nil {
		t.Fatal(err)
	}

	want := []string{"terminal", "send", "--terminal", "term_claude", "--text", "/work-item ci-failure org/a#60", "--enter", "--json"}
	if got := lastOrcaCall(t, stubs); !slices.Equal(got, want) {
		t.Errorf("orca %q\nwant %q", got, want)
	}
	for _, c := range stubs.Calls(t) {
		if c.Bin == "orca" && len(c.Args) > 1 && c.Args[0] == "terminal" && c.Args[1] == "create" {
			t.Errorf("started another Claude beside the live one: orca %q", c.Args)
		}
	}
}

// A live Claude that is not idle is busy: the Wake neither types into it
// nor starts a second Claude beside it, with or without a transcript.
func TestAWakeIntoALiveClaudeThatIsNotIdleIsBusy(t *testing.T) {
	for _, tc := range []struct {
		agents     []string
		transcript bool
	}{
		{[]string{"waiting"}, true},
		{[]string{"working"}, true},
		{[]string{"waiting"}, false},
	} {
		rt := running()
		orca, stubs := newOrca(t, rt)
		ws, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 7})
		if err != nil {
			t.Fatal(err)
		}
		if tc.transcript {
			haveTranscript(t, stubs, ws)
		}
		rt.AgentStates = map[string][]string{"issue-7": tc.agents}
		rt.Terminals = map[string][]testharness.OrcaTerminal{"issue-7": {{Handle: "term_claude", AgentIdentity: "claude"}}}
		stubs.SetFixture(t, testharness.Fixture{Orca: rt})

		err = orca.Wake(ctx, ws, "/work-item ci-failure org/a#60")

		if !errors.Is(err, workspace.ErrAgentBusy) {
			t.Errorf("%v, transcript %v: Wake() = %v, want ErrAgentBusy", tc.agents, tc.transcript, err)
		}
		for _, c := range stubs.Calls(t) {
			if c.Bin == "orca" && len(c.Args) > 1 && c.Args[0] == "terminal" && (c.Args[1] == "create" || c.Args[1] == "send") {
				t.Errorf("%v, transcript %v: orca %q into a busy Claude", tc.agents, tc.transcript, c.Args)
			}
		}
	}
}

func TestAWakeFallsBackToAFreshSessionWhenTheConversationCannotBeResumed(t *testing.T) {
	orca, stubs := newOrca(t, running())
	ws, err := orca.CreateForIssue(ctx, workspace.CreateInput{Repo: "org/a", Issue: 7})
	if err != nil {
		t.Fatal(err)
	}
	// No transcript: Claude never ran in this session, or has cleaned it up.

	if err := orca.Wake(ctx, ws, "/work-item ci-failure org/a#60"); err != nil {
		t.Fatal(err)
	}

	if got := lastOrcaCall(t, stubs); !slices.Contains(got, `claude --session-id `+ws.ClaudeSessionID+` "/work-item ci-failure org/a#60"`) {
		t.Errorf("orca %q; want a fresh session under the Workspace's session ID", got)
	}
}
