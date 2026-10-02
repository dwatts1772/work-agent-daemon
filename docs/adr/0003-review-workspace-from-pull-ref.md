# Review Workspaces are new local branches built from the fetched pull ref

The Orca CLI cannot check out an existing branch or a PR head. For a Review Request the daemon fetches `refs/pull/<n>/head` into `refs/remotes/origin/pr/<n>` (works for forks too) and creates the Workspace with `--base-branch origin/pr/<n>`, which makes a new, untracked local branch (e.g. `review-pr-<n>`). Because that branch never tracks the author's branch, a Review Workspace has no path to push to it — the "never modify the PR author's branch" rule is enforced structurally, not by policy.

## Considered Options

- Orca's private `worktree.create` RPC (`branchNameOverride`, `linkedPR`) — supports checking out the real branch, but is undocumented and would make the author's branch pushable.
- Plain `git worktree` outside Orca — works, but Review Workspaces would not appear in the Orca UI.
