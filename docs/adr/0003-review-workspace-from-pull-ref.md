# Review Workspaces are new local branches built from the fetched pull ref

The Orca CLI cannot check out an existing branch or a PR head. For a Review Request the daemon fetches `refs/pull/<n>/head` into `refs/remotes/origin/pr/<n>` (works for forks too) and creates the Workspace with `--base-branch origin/pr/<n>`, which makes a new, untracked local branch (e.g. `review-pr-<n>`). Because that branch never tracks the author's branch, a Review Workspace has no path to push to it — the "never modify the PR author's branch" rule is enforced structurally, not by policy.

## Consequences

"No path to push" covers `git push` and `git push origin`, which find no upstream. It does not cover an explicit refspec such as `git push origin HEAD:<author-branch>`: the Review Workspace shares its remotes with the Operator's clone, where Owned Issue Workspaces must push, so a non-pushing `pushurl` cannot be set for the Review Workspace alone, and it would not stop a push to a URL anyway. That case is left to the Entry Skill's "a Review Workspace never pushes, not even with an explicit refspec" guardrail (#38), pinned by `TestAnExplicitPushFromAReviewWorkspaceIsLeftToTheEntrySkill`.

## Considered Options

- Orca's private `worktree.create` RPC (`branchNameOverride`, `linkedPR`) — supports checking out the real branch, but is undocumented and would make the author's branch pushable.
- Plain `git worktree` outside Orca — works, but Review Workspaces would not appear in the Orca UI.
