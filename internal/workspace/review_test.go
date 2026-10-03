package workspace_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

func TestCreateForReviewFetchesThePullRefAndBranchesANewWorktreeFromIt(t *testing.T) {
	orca, stubs := newOrca(t, running())

	ws, err := orca.CreateForReview(ctx, workspace.ReviewInput{Repo: "org/b", PR: 333})
	if err != nil {
		t.Fatal(err)
	}

	var fetch, create []string
	for _, c := range stubs.Calls(t) {
		switch {
		case c.Bin == "git":
			fetch = c.Args
		case c.Bin == "orca" && len(c.Args) > 1 && c.Args[1] == "create":
			if fetch == nil {
				t.Errorf("worktree created before the pull ref was fetched")
			}
			create = c.Args
		}
	}
	clone := filepath.ToSlash(filepath.Join(stubs.Dir, "clones", "repo-b"))
	wantFetch := []string{"-C", clone, "fetch", "origin", "+refs/pull/333/head:refs/remotes/origin/pr/333"}
	if !slices.Equal(fetch, wantFetch) {
		t.Errorf("git %q, want git %q", fetch, wantFetch)
	}
	for _, want := range [][]string{{"--repo", "id:repo-b"}, {"--name", "review-pr-333"}, {"--base-branch", "origin/pr/333"}} {
		if i := slices.Index(create, want[0]); i < 0 || i+1 >= len(create) || create[i+1] != want[1] {
			t.Errorf("orca %q lacks %s %s", create, want[0], want[1])
		}
	}
	if !slices.Contains(create, "--no-parent") || slices.Contains(create, "--issue") || slices.Contains(create, "--agent") {
		t.Errorf("orca %q, want an independent worktree linked to no issue and starting no agent", create)
	}

	wts := stubs.OrcaWorktrees(t)
	if len(wts) != 1 || ws.OrcaIdentityKey != wts[0].IdentityKey || ws.Path != wts[0].Path || ws.Branch != "review-pr-333" {
		t.Errorf("Workspace = %+v, want the one review-pr-333 worktree of %+v", ws, wts)
	}
	if !uuidPattern.MatchString(ws.ClaudeSessionID) {
		t.Errorf("ClaudeSessionID = %q, want a daemon-generated UUID", ws.ClaudeSessionID)
	}
}

func TestCreateForReviewFailsForARepoOrcaDoesNotKnow(t *testing.T) {
	orca, stubs := newOrca(t, running())

	_, err := orca.CreateForReview(ctx, workspace.ReviewInput{Repo: "org/unregistered", PR: 1})

	if err == nil || !strings.Contains(err.Error(), "org/unregistered") {
		t.Errorf("err = %v, want one naming the unregistered repo", err)
	}
	for _, c := range stubs.Calls(t) {
		if c.Bin == "git" || (c.Bin == "orca" && slices.Contains(c.Args, "create")) {
			t.Errorf("ran %s %q for an unregistered repo", c.Bin, c.Args)
		}
	}
}

// realGit runs git for real and stands in for Orca 1.4, whose `worktree
// create --base-branch <ref>` runs `git worktree add --no-track -b <name>
// <path> <ref>` and sets push.autoSetupRemote (verified from its bundle).
type realGit struct {
	t     *testing.T
	clone string
}

func (g realGit) git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (g realGit) mustGit(dir string, args ...string) string {
	g.t.Helper()
	out, err := g.git(dir, args...)
	if err != nil {
		g.t.Fatalf("git %q: %v\n%s", args, err, out)
	}
	return out
}

func (g realGit) Run(_ context.Context, bin string, args []string, _ ...string) ([]byte, error) {
	if bin == "git" {
		out, err := g.git(args[1], args[2:]...)
		return []byte(out), err
	}
	ok := func(result any) ([]byte, error) {
		return json.Marshal(map[string]any{"ok": true, "result": result})
	}
	switch {
	case slices.Equal(args[:2], []string{"repo", "list"}):
		return ok(map[string]any{"repos": []any{map[string]any{
			"id": "repo-a", "path": g.clone,
			"gitRemoteIdentity": map[string]any{"canonicalKey": "github.com/org/a", "remoteName": "origin"},
		}}})
	case slices.Equal(args[:2], []string{"worktree", "create"}):
		name := args[slices.Index(args, "--name")+1]
		base := args[slices.Index(args, "--base-branch")+1]
		path := filepath.Join(filepath.Dir(g.clone), name)
		if out, err := g.git(g.clone, "worktree", "add", "--no-track", "-b", name, path, base); err != nil {
			return nil, fmt.Errorf("%v: %s", err, out)
		}
		g.mustGit(path, "config", "--local", "push.autoSetupRemote", "true")
		return ok(map[string]any{"worktree": map[string]any{
			"identity": map[string]any{"key": "wt2:local:review"}, "path": filepath.ToSlash(path), "branch": "refs/heads/" + name,
		}})
	}
	return nil, fmt.Errorf("unexpected orca %q", args)
}

func TestAReviewWorkspaceCannotPushToTheAuthorsBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	g := realGit{t: t, clone: filepath.Join(dir, "clone")}

	// GitHub: the author's branch "feature" is PR 333, whose head GitHub
	// also publishes as refs/pull/333/head.
	origin := filepath.Join(dir, "origin.git")
	g.mustGit(dir, "init", "--bare", "-b", "main", origin)
	g.mustGit(dir, "clone", origin, g.clone)
	g.mustGit(g.clone, "commit", "--allow-empty", "-m", "base")
	g.mustGit(g.clone, "push", "origin", "HEAD:main")
	g.mustGit(g.clone, "commit", "--allow-empty", "-m", "the author's change")
	g.mustGit(g.clone, "push", "origin", "HEAD:refs/heads/feature", "HEAD:refs/pull/333/head")
	g.mustGit(g.clone, "reset", "--hard", "origin/main")
	authorHead := g.mustGit(origin, "rev-parse", "refs/heads/feature")

	ws, err := workspace.NewOrca(g, t.TempDir()).CreateForReview(ctx, workspace.ReviewInput{Repo: "org/a", PR: 333})
	if err != nil {
		t.Fatal(err)
	}

	if got := g.mustGit(ws.Path, "rev-parse", "HEAD"); got != authorHead {
		t.Errorf("Review Workspace is at %s, want the PR head %s", got, authorHead)
	}
	if ws.Branch == "feature" {
		t.Errorf("Review Workspace is on the author's branch")
	}
	if out, err := g.git(ws.Path, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		t.Errorf("Review Workspace branch tracks %s, want no upstream", out)
	}

	g.mustGit(ws.Path, "commit", "--allow-empty", "-m", "a review fix")
	g.git(ws.Path, "push")
	g.git(ws.Path, "push", "origin")
	if got := g.mustGit(origin, "rev-parse", "refs/heads/feature"); got != authorHead {
		t.Errorf("a push from the Review Workspace moved the author's branch to %s", got)
	}
}
