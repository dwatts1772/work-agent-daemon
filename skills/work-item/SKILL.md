---
name: work-item
description: Entry Skill for a work-agent Wake — routes `/work-item <issue|feedback|ci-failure|review> <owner/repo>#<n>` to the right workflow.
argument-hint: <issue|feedback|ci-failure|review> <owner/repo>#<n>
disable-model-invocation: true
---

# /work-item

The work-agent daemon Wakes you in a Workspace with `/work-item <reason> <owner/repo>#<n>`. This skill picks the workflow and holds the push policy around it; the routed skill does the engineering, so hand the Work Item to it and let it run.

## 1. Inspect

1. Parse the arguments. The Wake Reason is one of `issue`, `feedback`, `ci-failure`, `review`; `<n>` is an issue for `issue` and a pull request for the rest. Anything else → stop and tell the Operator what was received.
2. Read the Workspace's git state: `git status`, `git branch --show-current`, `git log --oneline -5`, and the branch's upstream. A `review` Wake runs in a Review Workspace (a local `review-pr-<n>` branch with no upstream); every other Wake runs in the Owned Issue's Workspace on the item's own branch.
3. Read the Work Item on GitHub:
   - `issue` — `gh issue view <n> --repo <repo> --comments`, and any PR already open from this branch: `gh pr list --repo <repo> --head <branch> --state all`.
   - `feedback` — `gh pr view <n> --repo <repo> --comments`, and the unresolved review threads: `gh api graphql` on `pullRequest.reviewThreads`, keeping those with `isResolved: false`.
   - `ci-failure` — `gh pr checks <n> --repo <repo>`, then `gh run view <run-id> --repo <repo> --log-failed` for each failing run on the head commit.
   - `review` — `gh pr view <n> --repo <repo> --json headRefOid,reviews`, then [Re-review](#4-re-review) decides between `first` and `new-head`.

Done when you can name the Wake Reason, the Situation (as defined in `routing.md`), and the Workspace's branch and whether its tree is clean.

## 2. Resolve Routing

Routing has three layers. Read each one that exists, in this order; skip a missing file silently:

1. `routing.md` beside this file — the built-in default (mattpocock-skills).
2. `~/.work-agent/routing.md` — the Operator override.
3. `docs/agents/work-item-routing.md` in the target repo (the Workspace root) — the repo override.

Every layer uses the table format of `routing.md`. Merge them row by row: a later row with the same Wake Reason and Situation replaces the earlier one, a new Situation adds a row, and rows a layer leaves out are inherited. Prose in a layer follows the same precedence — the repo override wins over the Operator override, which wins over the default.

Pick the row matching the Wake Reason and Situation; a specific Situation beats `any`. When none matches, or two specific Situations fit equally, stop and ask the Operator which route to take.

## 3. Follow the route

Invoke the routed skill with the Work Item as its input and let it run to its own completion. A skill with model invocation disabled (e.g. `/implement`) is followed by reading its `SKILL.md` from the installed plugin. A routed skill that is not installed → stop and tell the Operator which one is missing.

Done when the route's last step is complete and every push, PR, and review it produced follows the push policy.

## 4. Re-review

Find the last head the Operator reviewed: the head SHA recorded in held findings at `$(git rev-parse --git-dir)/work-item/review.md`, or the `commit_id` of the Operator's latest submitted review. The Situation is `new-head` when that SHA differs from the PR's `headRefOid` on GitHub, and `first` when there is no such SHA.

On a re-review, before following the route:

1. Move the Workspace to the new head (the daemon has already fetched it): `git reset --keep origin/pr/<n>`, then confirm `HEAD` equals `headRefOid`.
2. Scope the new diff: `git diff <last-reviewed-sha>..HEAD`. When the author rewrote history and the last reviewed SHA is gone, review the full PR diff and say so in the findings.
3. Collect the prior findings — the held ones and the Operator's review threads still `isResolved: false` — and check each against the new head.

Done when every prior finding is marked resolved or still open, and the held findings cover only the new diff plus the unresolved prior findings.

## Push policy

These rules hold under every Routing layer and every routed skill.

**Owned Issue** — push only the Workspace's own branch, as a plain fast-forward: `git push -u origin HEAD`. Open pull requests with `gh pr create --draft`. Marking a PR ready for review is the Operator's call: when the work is done, tell the Operator the draft is ready for them.

**Review Request** — a Review Workspace never pushes, not even with an explicit refspec such as `git push origin HEAD:<branch>`: its branch has no upstream (ADR-0003), but git would still accept an explicit one, so this rule is what keeps the PR author's branch untouched. The review lives in findings, not commits.

**Held for Operator approval** — review findings, and replies to review threads (PRD §12 Operator gate), are held for Operator approval. Write them to `$(git rev-parse --git-dir)/work-item/review.md` with the head SHA they were written against, present them to the Operator, and wait for an explicit approval in this conversation. Then submit exactly what the Operator approved, with the verdict the Operator chose.

**Guardrail** — never `--force`, `--force-with-lease`, or a `+` refspec on a push; never `gh pr ready`, `gh pr merge`, or a review submission the Operator has not approved. When a route asks for one of these, stop and hand that step to the Operator.
