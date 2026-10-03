package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
)

// orca mimics the Orca 1.4 CLI's --json envelopes: {"ok":true,"result":…}
// on success, {"ok":false,"error":{…}} with exit 1 on failure.
func orca(dir string, rt *testharness.Orca, args []string) {
	if len(args) >= 1 && args[0] == "status" {
		orcaStatus(rt)
		return
	}
	if rt == nil {
		orcaFail("runtime_unavailable", "Orca is not running. Run `orca open` first.")
	}
	switch {
	case len(args) >= 2 && args[0] == "repo" && args[1] == "list":
		orcaRepoList(dir, rt)
	case len(args) >= 2 && args[0] == "worktree" && args[1] == "create":
		orcaWorktreeCreate(dir, rt, args[2:])
	case len(args) >= 2 && args[0] == "worktree" && args[1] == "show":
		orcaWorktreeShow(dir, args[2:])
	case len(args) >= 2 && args[0] == "worktree" && args[1] == "ps":
		orcaWorktreePs(dir, rt)
	case len(args) >= 2 && args[0] == "terminal" && args[1] == "create":
		orcaTerminalCreate(dir, args[2:])
	case len(args) >= 2 && args[0] == "terminal" && args[1] == "list":
		orcaTerminalList(dir, rt, args[2:])
	case len(args) >= 2 && args[0] == "terminal" && args[1] == "send":
		orcaTerminalSend(rt, args[2:])
	default:
		fail(2, "stub orca: unsupported command %q", args)
	}
}

// orcaStatus reports reachability the way real Orca does: always ok, with
// runtime.reachable false when no runtime is running.
func orcaStatus(rt *testharness.Orca) {
	runtime := map[string]any{"state": "not_running", "reachable": false, "runtimeId": nil}
	if rt != nil {
		runtime = map[string]any{"state": "ready", "reachable": true, "runtimeId": "stub-runtime"}
	}
	orcaOK(map[string]any{"app": map[string]any{"running": rt != nil}, "runtime": runtime})
}

func orcaRepoList(dir string, rt *testharness.Orca) {
	repos := []map[string]any{}
	for id, ghRepo := range rt.Repos {
		repos = append(repos, map[string]any{
			"id":                id,
			"path":              filepath.ToSlash(filepath.Join(dir, "clones", id)),
			"displayName":       ghRepo,
			"gitRemoteIdentity": map[string]any{"canonicalKey": "github.com/" + ghRepo, "remoteName": "origin"},
		})
	}
	orcaOK(map[string]any{"repos": repos})
}

func orcaWorktreeCreate(dir string, rt *testharness.Orca, args []string) {
	fs := flag.NewFlagSet("worktree create", flag.ContinueOnError)
	repo := fs.String("repo", "", "")
	name := fs.String("name", "", "")
	issue := fs.Int("issue", 0, "")
	base := fs.String("base-branch", "", "")
	fs.Bool("no-parent", false, "")
	fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		fail(2, "stub orca: %v", err)
	}
	repoID, ok := strings.CutPrefix(*repo, "id:")
	if _, known := rt.Repos[repoID]; !ok || !known {
		orcaFail("selector_not_found", "repo "+*repo+" not found")
	}
	wts := loadWorktrees(dir)
	for _, wt := range wts {
		if wt.RepoID == repoID && wt.Name == *name {
			orcaFail("branch_exists", "a branch named "+*name+" already exists")
		}
	}
	instance := fmt.Sprintf("00000000-0000-4000-8000-%012d", len(wts)+1)
	wt := testharness.OrcaWorktree{
		IdentityKey: "wt2:local:" + instance,
		RepoID:      repoID,
		Name:        *name,
		Path:        filepath.ToSlash(filepath.Join(dir, "workspaces", repoID, *name)),
		Branch:      "refs/heads/" + *name,
		Issue:       *issue,
		BaseBranch:  *base,
	}
	saveWorktrees(dir, append(wts, wt))
	orcaOK(map[string]any{"worktree": worktreeJSON(wt)})
}

func orcaWorktreeShow(dir string, args []string) {
	wt := findWorktree(dir, args)
	orcaOK(map[string]any{"worktree": worktreeJSON(wt)})
}

func orcaWorktreePs(dir string, rt *testharness.Orca) {
	out := []map[string]any{}
	for _, wt := range loadWorktrees(dir) {
		agents := []map[string]any{}
		for _, s := range rt.AgentStates[wt.Name] {
			agents = append(agents, map[string]any{"state": s, "agentType": "claude"})
		}
		out = append(out, map[string]any{
			"worktreeId":         wt.RepoID + "::" + wt.Path,
			"repoId":             wt.RepoID,
			"path":               wt.Path,
			"branch":             wt.Branch,
			"worktreeInstanceId": strings.TrimPrefix(wt.IdentityKey, "wt2:local:"),
			"agents":             agents,
		})
	}
	orcaOK(map[string]any{"worktrees": out, "totalCount": len(out), "truncated": false})
}

func orcaTerminalCreate(dir string, args []string) {
	wt := findWorktree(dir, args)
	orcaOK(map[string]any{"terminal": map[string]any{"handle": "term_stub", "worktreeId": wt.RepoID + "::" + wt.Path}})
}

func orcaTerminalList(dir string, rt *testharness.Orca, args []string) {
	wt := findWorktree(dir, args)
	out := []map[string]any{}
	for _, term := range rt.Terminals[wt.Name] {
		t := map[string]any{"handle": term.Handle, "worktreePath": wt.Path, "connected": true, "writable": true, "orphaned": false}
		if term.AgentIdentity != "" {
			t["agentIdentity"] = term.AgentIdentity
		}
		out = append(out, t)
	}
	orcaOK(map[string]any{"terminals": out, "totalCount": len(out), "truncated": false})
}

// orcaTerminalSend accepts input for any live terminal handle.
func orcaTerminalSend(rt *testharness.Orca, args []string) {
	var handle string
	for i, a := range args {
		if a == "--terminal" && i+1 < len(args) {
			handle = args[i+1]
		}
	}
	for _, terms := range rt.Terminals {
		for _, term := range terms {
			if term.Handle == handle {
				orcaOK(map[string]any{"handle": handle, "accepted": true})
				return
			}
		}
	}
	orcaFail("terminal_not_found", "terminal "+handle+" not found")
}

// findWorktree resolves the --worktree identity:<key> selector in args.
func findWorktree(dir string, args []string) testharness.OrcaWorktree {
	var selector string
	for i, a := range args {
		if a == "--worktree" && i+1 < len(args) {
			selector = args[i+1]
		}
	}
	key, ok := strings.CutPrefix(selector, "identity:")
	if !ok {
		fail(2, "stub orca: the daemon must select worktrees by identity, got %q", selector)
	}
	for _, wt := range loadWorktrees(dir) {
		if wt.IdentityKey == key {
			return wt
		}
	}
	orcaFail("selector_not_found", "selector_not_found")
	panic("unreachable")
}

func worktreeJSON(wt testharness.OrcaWorktree) map[string]any {
	return map[string]any{
		"id":          wt.RepoID + "::" + wt.Path,
		"identity":    map[string]any{"key": wt.IdentityKey, "executionHostId": "local"},
		"repoId":      wt.RepoID,
		"path":        wt.Path,
		"branch":      wt.Branch,
		"displayName": wt.Name,
		"linkedIssue": wt.Issue,
	}
}

func loadWorktrees(dir string) []testharness.OrcaWorktree {
	data, err := os.ReadFile(filepath.Join(dir, testharness.OrcaWorktreesFile))
	if os.IsNotExist(err) {
		return nil
	}
	var wts []testharness.OrcaWorktree
	if err == nil {
		err = json.Unmarshal(data, &wts)
	}
	if err != nil {
		fail(1, "stub orca: worktrees: %v", err)
	}
	return wts
}

func saveWorktrees(dir string, wts []testharness.OrcaWorktree) {
	data, _ := json.Marshal(wts)
	if err := os.WriteFile(filepath.Join(dir, testharness.OrcaWorktreesFile), data, 0o600); err != nil {
		fail(1, "stub orca: worktrees: %v", err)
	}
}

func orcaOK(result any) {
	out, _ := json.Marshal(map[string]any{"id": "stub", "ok": true, "result": result})
	fmt.Println(string(out))
}

func orcaFail(code, message string) {
	out, _ := json.Marshal(map[string]any{"id": "stub", "ok": false, "error": map[string]any{"code": code, "message": message}})
	fmt.Println(string(out))
	os.Exit(1)
}
