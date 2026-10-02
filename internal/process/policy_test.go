package process

import "testing"

func TestCheckAllowsReadOnlyGitHubCalls(t *testing.T) {
	allowed := [][]string{
		{"auth", "token", "--user", "dwatts1772"},
		{"api", "user"},
		{"api", "--method", "GET", "repos/o/r/pulls"},
		{"issue", "list", "--repo", "o/r", "--assignee", "dwatts1772"},
		{"api", "-XGET", "repos/o/r"},
		{"api", "-iXGET", "repos/o/r"},
		{"api", "-H", "Accept: -XPUT", "repos/o/r"},
		{"api", "-q.login", "user"},
	}
	for _, args := range allowed {
		if err := Check("gh", args); err != nil {
			t.Errorf("Check(gh %v) = %v, want allowed", args, err)
		}
	}
}

func TestCheckRejectsMutatingGitHubCalls(t *testing.T) {
	rejected := [][]string{
		{},
		{"pr", "merge", "4"},
		{"pr", "merge", "4", "--admin", "--squash"},
		{"pr", "ready", "4"},
		{"pr", "review", "4", "--approve"},
		{"issue", "close", "4"},
		{"repo", "delete", "o/r"},
		{"auth", "switch"},
		{"auth", "login"},
		{"api", "-X", "PUT", "repos/o/r/pulls/4/merge"},
		{"api", "--method=PUT", "repos/o/r/pulls/4/merge"},
		{"api", "--method", "post", "repos/o/r/issues"},
		{"api", "repos/o/r/issues", "-f", "title=x"},
		{"api", "repos/o/r/issues", "--raw-field", "title=x"},
		{"api", "repos/o/r/issues", "-Fbody=x"},
		{"api", "graphql", "--input", "q.json"},
		{"api", "-iXPUT", "repos/o/r/pulls/4/merge"},
		{"api", "-i", "-X", "PUT", "repos/o/r/pulls/4/merge"},
		{"api", "-iffoo=bar", "repos/o/r/issues"},
		{"api", "-X"},
		{"api", "--hostname", "evil.example", "user"},
		{"pr", "list", "--repo", "o/r"},
		{"extension", "exec", "x"},
		{"alias", "set", "m", "pr merge"},
	}
	for _, args := range rejected {
		if err := Check("gh", args); err == nil {
			t.Errorf("Check(gh %v) allowed, want rejected", args)
		}
	}
}

func TestCheckAllowsSafeGitCalls(t *testing.T) {
	allowed := [][]string{
		{"fetch", "origin", "+refs/pull/4/head:refs/remotes/origin/pr/4"},
		{"-C", "/work/a", "rev-parse", "HEAD"},
		{"status", "--porcelain"},
	}
	for _, args := range allowed {
		if err := Check("git", args); err != nil {
			t.Errorf("Check(git %v) = %v, want allowed", args, err)
		}
	}
}

func TestCheckRejectsForcePushAndUnknownGitCalls(t *testing.T) {
	rejected := [][]string{
		{},
		{"push", "origin", "HEAD"},
		{"push", "--force"},
		{"push", "--force-w", "origin", "main"},
		{"push", "--mirr"},
		{"push", "--del", "origin", "main"},
		{"push", "-f", "origin", "main"},
		{"push", "-uf", "origin", "main"},
		{"push", "--force-with-lease", "origin", "main"},
		{"push", "--force-with-lease=main", "origin", "main"},
		{"push", "--force-if-includes", "origin", "main"},
		{"push", "--mirror"},
		{"push", "--delete", "origin", "main"},
		{"push", "-d", "origin", "main"},
		{"push", "--prune", "origin"},
		{"push", "origin", "+main"},
		{"push", "origin", "+HEAD:main"},
		{"push", "origin", ":main"},
		{"-C", "/work/a", "push", "--force"},
		{"-c", "alias.x=!rm -rf /", "x"},
		{"fetch", "--upload-pack=sh -c evil", "origin"},
		{"fetch", "--upload-pack", "sh -c evil", "origin"},
		{"fetch", "--upl=evil", "origin"},
		{"-C", "/work/a", "fetch", "--exec=evil"},
		{"ls-remote", "-u", "evil", "origin"},
		{"reset", "--hard"},
		{"branch", "-D", "main"},
		{"worktree", "remove", "x"},
		{"merge", "feat"},
	}
	for _, args := range rejected {
		if err := Check("git", args); err == nil {
			t.Errorf("Check(git %v) allowed, want rejected", args)
		}
	}
}

func TestCheckRejectsUnknownBinaries(t *testing.T) {
	for _, bin := range []string{"sh", "bash", "cmd", "powershell", "curl", ""} {
		if err := Check(bin, []string{"-c", "echo hi"}); err == nil {
			t.Errorf("Check(%q) allowed, want rejected", bin)
		}
	}
}

func TestCheckAllowsOrcaAndClaude(t *testing.T) {
	if err := Check("orca", []string{"status", "--json"}); err != nil {
		t.Errorf("orca: %v", err)
	}
	if err := Check("claude", []string{"--session-id", "x"}); err != nil {
		t.Errorf("claude: %v", err)
	}
}
