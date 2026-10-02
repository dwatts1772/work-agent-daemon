# Work Agent Daemon — Initial PRD

## 1. Summary

Build a small local daemon for macOS that watches GitHub for work assigned to the developer and PR state changes, then uses Orca to create or resume an isolated worktree and launches Claude Code with the correct entry prompt.

The daemon itself contains no LLM and performs no engineering judgment. GitHub is the source of truth for work state. Orca owns local worktrees/sessions. Claude Code owns planning, implementation, review, and feedback handling using existing Matt Pocock and pstack-style skills.

### Core idea

```text
GitHub / Slack
      │
      ▼
local workflow daemon
(no LLM)
      │
      ▼
workflow state machine
      │
      ├── issue assigned ─────────────┐
      │                                │
      ├── review requested on PR ──────┤
      │                                │
      └── PR feedback / CI ────────────┤
                                       ▼
                              Orca
                                │
                      create/resume worktree
                                │
                                ▼
                           Claude Code
                                │
                   Matt + pstack + custom skills
```

## 2. Goals

1. Remove repetitive manual Orca UI actions.
2. Automatically create an Orca worktree when an eligible GitHub issue is assigned.
3. Launch Claude Code in that worktree with a deterministic entry prompt.
4. Track issue → worktree → PR relationships durably.
5. Detect PR review feedback and CI state changes.
6. Detect PRs opened by other developers where the current user is requested as a reviewer.
7. Launch an isolated Claude Code review session for incoming review requests.
8. Re-run review when a reviewed PR receives a new head SHA and review is still requested.
9. Resume the correct worktree/session when new work appears.
10. Keep engineering decisions inside Claude Code, not the daemon.
11. Avoid additional model/API usage while idle.
12. Preserve human approval for sensitive actions such as merge and optionally push/replies.
13. Be small enough to understand, modify, and run locally on a managed work Mac.

## 3. Non-goals

MVP will not:

- replace Claude Code
- replace Matt Pocock skills
- replace Orca as the local worktree/session UI
- decide architecture or implementation strategy
- merge PRs automatically
- deploy code
- manage multiple developers
- expose a public webhook server
- depend on a hosted backend
- require an LLM outside Claude Code

## 4. Primary workflow

### A. New assigned issue

```text
GitHub issue assigned to me
        ↓
daemon discovers issue
        ↓
verify issue is eligible
        ↓
create local workflow record
        ↓
call Orca CLI
        ↓
create issue worktree
        ↓
launch Claude Code
        ↓
/work-item issue <repo>#<number>
        ↓
Claude determines next engineering step
```

The `/work-item` skill owns routing inside Claude Code.

Example routing:

```text
unclear feature
  → grill-with-docs
  → to-spec
  → to-tickets

implementation-ready issue
  → implement

bug
  → reproduce / investigate
  → implement
```

### B. PR opened

Claude Code creates the PR through the existing workflow.

The daemon discovers the PR and attaches it to the existing workflow record:

```text
issue #428
worktree issue-428
PR #901
state WAITING_FOR_REVIEW
```

### C. Review feedback

```text
new unresolved PR review feedback
        ↓
daemon detects unseen comment/review
        ↓
locate workflow record + Orca worktree
        ↓
resume/start Claude Code in worktree
        ↓
/work-item feedback <PR>
        ↓
Claude runs address-feedback workflow
        ↓
verify
        ↓
optional approval gate
        ↓
push/reply/notify
        ↓
WAITING_FOR_REVIEW
```

### D. CI failure

```text
CI transitions to failed
        ↓
dedupe by PR head SHA + check/run id
        ↓
resume Claude Code
        ↓
/work-item ci-failure <PR>
        ↓
Claude investigates and fixes
```

Do not wake Claude for repeated observations of the same failure.

### E. Ready to merge

When:

- required CI is green
- no actionable unresolved review feedback remains
- PR is open

set:

```text
READY_TO_MERGE
```

Notify the developer. Never merge automatically in MVP.

### F. Incoming PR review request

The daemon must also watch for PRs opened by other developers where the current user is requested as a reviewer.

```text
PR #333 requests my review
        ↓
daemon discovers review request
        ↓
dedupe by repo + PR + head SHA
        ↓
create review workflow record
        ↓
create isolated review worktree
        ↓
launch Claude Code
        ↓
/work-item review <repo>#333
        ↓
Claude runs /code-review PR#333
        ↓
produce review findings
        ↓
human approval before submitting review
        ↓
REVIEW_COMPLETE
```

Review requests are a separate workflow type from owned issues.

If the author pushes a new commit after review:

```text
reviewed head SHA = abc123
current head SHA = def456
review is still requested
        ↓
wake the same review workspace
        ↓
/work-item review <repo>#333
        ↓
Claude reviews the new diff / unresolved concerns
```

The daemon must never submit an approval, request-changes review, or review comment without the configured human approval gate in MVP.

## 5. State machine

```ts
type WorkState =
  | "DISCOVERED"
  | "STARTING"
  | "PLANNING"
  | "IMPLEMENTING"
  | "WAITING_FOR_CI"
  | "WAITING_FOR_REVIEW"
  | "ADDRESSING_FEEDBACK"
  | "REVIEWING_EXTERNAL_PR"
  | "REVIEW_COMPLETE"
  | "BLOCKED"
  | "READY_TO_MERGE"
  | "DONE"
  | "FAILED";
```

The daemon should not attempt to infer detailed engineering progress. These states describe orchestration status only.

### Suggested transitions

```text
DISCOVERED
  → STARTING
  → PLANNING | IMPLEMENTING

PLANNING
  → IMPLEMENTING | BLOCKED

IMPLEMENTING
  → WAITING_FOR_CI | WAITING_FOR_REVIEW | BLOCKED

WAITING_FOR_CI
  → IMPLEMENTING          on failure requiring changes
  → WAITING_FOR_REVIEW    on green

WAITING_FOR_REVIEW
  → ADDRESSING_FEEDBACK   on actionable feedback
  → READY_TO_MERGE        when review + CI conditions satisfied

ADDRESSING_FEEDBACK
  → WAITING_FOR_CI | WAITING_FOR_REVIEW

READY_TO_MERGE
  → DONE                  after external merge detected

REVIEWING_EXTERNAL_PR
  → REVIEW_COMPLETE        after review is prepared/submitted per policy
  → BLOCKED | FAILED

REVIEW_COMPLETE
  → REVIEWING_EXTERNAL_PR  when head SHA changes and review is still requested
  → DONE                   when review request is removed, PR closes, or PR merges

any active state
  → BLOCKED | FAILED
```

## 6. Event model

MVP should poll GitHub rather than expose webhooks.

Recommended interval: 30–60 seconds.

### Events

```ts
type WorkflowEvent =
  | { type: "ISSUE_ASSIGNED"; repo: string; issue: number }
  | { type: "PR_DISCOVERED"; repo: string; issue: number; pr: number }
  | { type: "PR_REVIEW_FEEDBACK"; repo: string; pr: number; marker: string }
  | { type: "REVIEW_REQUESTED"; repo: string; pr: number; headSha: string; requestedBy?: string }
  | { type: "REVIEW_REQUEST_REMOVED"; repo: string; pr: number }
  | { type: "REVIEW_HEAD_CHANGED"; repo: string; pr: number; oldHeadSha: string; headSha: string }
  | { type: "CI_FAILED"; repo: string; pr: number; headSha: string; marker: string }
  | { type: "CI_GREEN"; repo: string; pr: number; headSha: string }
  | { type: "PR_MERGED"; repo: string; pr: number }
  | { type: "PR_CLOSED"; repo: string; pr: number };
```

Every event must have a stable dedupe marker.

Examples:

- issue assignment: `repo#issue:assigned:<updatedAt>`
- review request: `repo#pr:review-request:<reviewer>:<headSha>`
- review head change: `repo#pr:review-head:<headSha>`
- review comment: GitHub comment ID
- review: review ID
- CI: `<headSha>:<checkRunId>:<conclusion>`
- merge: merge commit SHA

## 7. Durable data

For the first weekend MVP, use a local JSON state file to avoid adding a database dependency.

```text
~/.work-agent/
  config.json
  state.json
  logs/
```

Move to SQLite later if concurrent writes/history become useful.

### Workflow record

```ts
type WorkItem =
  | OwnedIssueWorkItem
  | ReviewRequestWorkItem;

interface BaseWorkItem {
  id: string;
  repo: string;
  state: WorkState;

  branch?: string;
  worktreeName?: string;
  worktreePath?: string;
  orcaWorktreeId?: string;
  claudeSessionId?: string;

  processedEventIds: string[];

  createdAt: string;
  updatedAt: string;
  lastWakeAt?: string;
  lastError?: string;
}

interface OwnedIssueWorkItem extends BaseWorkItem {
  kind: "OWNED_ISSUE";
  issue: number;
  issueUrl: string;

  pr?: number;
  prUrl?: string;
  headSha?: string;
}

interface ReviewRequestWorkItem extends BaseWorkItem {
  kind: "REVIEW_REQUEST";
  pr: number;
  prUrl: string;
  requestedBy?: string;

  headSha: string;
  lastReviewedHeadSha?: string;
}
```

Use atomic state writes:

1. write `state.json.tmp`
2. `fsync` if desired
3. rename over `state.json`

## 8. Configuration

```json
{
  "github": {
    "repos": ["org/project-a"],
    "assignee": "@me",
    "pollIntervalSeconds": 45
  },
  "orca": {
    "enabled": true
  },
  "claude": {
    "entrySkill": "/work-item"
  },
  "automation": {
    "autoCreateWorktree": true,
    "autoCreateReviewWorktree": true,
    "autoWakeOnReview": true,
    "autoWakeOnReviewRequest": true,
    "autoWakeOnReviewHeadChange": true,
    "autoWakeOnCiFailure": true,
    "autoMerge": false
  }
}
```

Future configuration:

- label allowlist such as `agent-ready`
- repo-specific settings
- quiet hours
- max concurrent Claude workers
- Slack notifications
- approval policy

## 9. Integrations

### GitHub

Use the existing `gh` CLI rather than a GitHub SDK for MVP.

Responsibilities:

- list issues assigned to current user
- fetch issue details
- find linked PRs
- list open PRs where the current user is requested as a reviewer
- fetch PR author, head SHA, reviews, review requests, and comments
- detect when a reviewed PR receives a new head SHA
- inspect CI/check status
- detect merged/closed PRs

Prefer JSON output from `gh` and parse it locally.

### Orca

Use Orca CLI as the execution backend.

Required capabilities:

- create worktree from issue/current repo
- identify existing worktrees
- launch Claude Code in a worktree
- pass an initial prompt
- resume/relaunch an existing workspace when new work arrives

Wrap all Orca interaction behind an adapter:

```ts
interface WorkspaceBackend {
  createForIssue(input: CreateWorkspaceInput): Promise<Workspace>;
  createForReview(input: CreateReviewWorkspaceInput): Promise<Workspace>;
  wake(workspace: Workspace, prompt: string): Promise<void>;
  exists(workspace: Workspace): Promise<boolean>;
}
```

This keeps Orca replaceable later.

### Claude Code

The daemon should not directly encode Matt/pstack workflow logic.

It launches Claude with one entry skill:

```text
/work-item issue org/project-a#428
/work-item feedback org/project-a#901
/work-item ci-failure org/project-a#901
/work-item review org/project-a#333
```

The skill decides which underlying skill to invoke.

### Slack

Phase 2.

Use Slack only for:

- PR ready notifications
- feedback addressed notifications
- blocked/failed notifications
- ready-to-merge notifications

Do not use Slack as workflow state.

## 10. `/work-item` Claude skill

Create one thin custom routing skill.

Responsibilities:

1. Inspect the supplied GitHub issue/PR.
2. Inspect the local branch/worktree state.
3. Determine the workflow type.
4. Route to existing skills.
5. Never duplicate the implementation of those skills.

Suggested routing:

```text
issue + unclear requirements
  → grill-with-docs

issue + large feature requiring decomposition
  → grill-with-docs
  → to-spec
  → to-tickets

issue + implementation ready
  → implement

PR + review feedback
  → address-feedback

PR + CI failure
  → investigate failure
  → fix + verify

PR + review requested from me
  → code-review PR#<number>

PR + review requested + new head SHA
  → code-review PR#<number> against latest head
  → focus on new diff and unresolved prior findings

PR status / outstanding work
  → pstack babysit semantics
```

The daemon only supplies context and wake reason.

## 11. Concurrency

MVP default:

```text
maxConcurrentWorkers = 1
```

Reason: prove lifecycle correctness before parallelizing.

Phase 2 can allow 2–3 independent issue worktrees.

Never allow two daemon invocations to wake the same worktree simultaneously.

Use an in-memory lock plus persisted `lastWakeAt`/state guard.

## 12. Safety / professional environment

Required safeguards:

- repo allowlist
- only auto-implement issues assigned to current user
- only auto-review PRs where the current user is explicitly requested as reviewer
- never treat a review request as authorization to modify the PR author's branch
- optional required label such as `agent-ready`
- never merge
- never force push
- never deploy
- never delete branches/worktrees automatically in MVP
- never send customer/public messages
- log every external action
- redact secrets from logs
- invoke only known binaries with argument arrays; avoid shell interpolation
- daemon runs as the logged-in developer, not root

Recommended initial approval boundary:

```text
Automatic:
- create worktree
- launch Claude
- run tests
- inspect GitHub
- open draft PR if current workflow already permits it

Human gate:
- push, if desired
- reply to GitHub review, if desired
- Slack message, if desired
- merge always
```

## 13. CLI

```bash
work-agent start
work-agent stop
work-agent status
work-agent list
work-agent inspect org/project-a#428
work-agent wake org/project-a#428
work-agent pause org/project-a#428
work-agent resume org/project-a#428
work-agent logs org/project-a#428
```

Useful dev command:

```bash
work-agent tick
```

Runs exactly one polling/reconciliation cycle and exits. This should be the primary way to develop/debug the daemon.

## 14. Internal modules

```text
src/
  cli.ts
  daemon.ts
  config.ts
  state.ts

  github/
    client.ts
    discover.ts
    events.ts

  workspace/
    backend.ts
    orca.ts

  workflow/
    reducer.ts
    reconcile.ts
    handlers.ts

  process/
    run.ts

  notify/
    console.ts
    slack.ts          # phase 2
```

### Important rule

Make the workflow reducer pure:

```ts
const actions = reduce(currentState, event);
```

Example output:

```ts
[
  { type: "CREATE_WORKSPACE" },
  { type: "WAKE_CLAUDE", prompt: "/work-item issue org/project-a#428" }
]
```

Handlers execute actions. This makes the orchestration logic easy to test without GitHub, Orca, or Claude.

## 15. Reconciliation model

Do not rely only on edge-triggered events.

Every poll should reconcile observed truth against persisted state.

Example:

```text
state says WAITING_FOR_REVIEW
GitHub says PR merged
→ transition DONE
```

This allows recovery after:

- daemon restart
- Mac sleep
- network outage
- missed poll
- Orca restart

## 16. Logging

Structured JSONL is enough.

```json
{"time":"...","level":"info","item":"org/project-a#428","event":"PR_REVIEW_FEEDBACK","action":"WAKE_CLAUDE"}
```

Keep a human-readable console formatter too.

## 17. macOS daemon

Run under `launchd`.

MVP can start as:

```bash
node dist/cli.js start
```

Once stable, add a user LaunchAgent plist so it starts after login and restarts on failure.

Do not daemonize/fork in Node. Let `launchd` supervise the process.

## 18. MVP acceptance criteria

The weekend MVP is successful when all of the following work:

1. `work-agent tick` discovers a newly assigned issue from an allowlisted repo.
2. It creates exactly one Orca worktree for that issue.
3. Re-running the tick does not create duplicates.
4. Claude Code launches with `/work-item issue <repo>#<issue>`.
5. Once a PR exists, the daemon associates it with the issue/worktree.
6. New review feedback causes the same worktree to wake exactly once.
7. Re-observing the same review comment does nothing.
8. A CI failure wakes the same worktree exactly once per unique failure/head SHA.
9. Restarting the daemon preserves all associations and dedupe state.
10. A merged PR transitions the item to `DONE` without launching Claude.
11. A PR opened by another developer with the current user requested as reviewer creates exactly one review workflow/worktree for that PR head SHA.
12. Claude launches in that review workspace with `/work-item review <repo>#<pr>`, which routes to `/code-review PR#<pr>`.
13. Re-observing the same review request/head SHA does not create or wake another session.
14. If the PR head SHA changes while review is still requested, the existing review workspace wakes exactly once for the new SHA.
15. Review workflows never modify the PR author's branch.
16. Review submission remains human-gated in MVP.
17. No code path can merge, force push, or deploy.

## 19. Weekend implementation plan

### Friday / setup

- scaffold TypeScript project
- implement config loader
- implement process runner
- implement `state.json` store
- implement `work-agent tick`

### Saturday morning

- GitHub assigned issue discovery via `gh`
- workflow reducer
- event dedupe
- console logging

### Saturday afternoon

- Orca adapter
- create worktree from issue
- launch Claude with `/work-item`
- verify idempotency

### Sunday morning

- PR discovery/linking
- incoming review-request discovery
- dedicated review worktree/session flow
- PR head-SHA change detection for re-review
- PR review comment/review detection
- CI status detection
- wake existing worktree

### Sunday afternoon

- restart/recovery testing
- launchd config
- safety guards
- README
- dogfood on one real low-risk issue

## 20. Phase 2

After MVP proves reliable:

- Slack notifications
- multiple concurrent work items
- required label / command controls
- SQLite event history
- richer `status` UI
- automatic draft PR detection
- stale worktree cleanup
- quiet hours
- pause/resume from Slack
- GitHub webhooks instead of polling
- project-specific workflow policies
- pstack `babysit` inspired review/CI loop
- review-request debounce / batching
- optional automatic review submission policy for trusted repos
- optional local web dashboard

## 21. Open design questions

Decide while building, not before:

1. Can Orca reliably identify/resume a worktree by stable CLI ID, or should the daemon persist path/name only?
2. Should issue assignment immediately launch Claude, or require a label such as `agent-ready`?
3. Should Claude be allowed to push automatically or stop before push?
4. Should the daemon wake on every CI failure or only after all checks settle?
5. Should review feedback be grouped for a short debounce window before waking Claude?
6. Does the existing `address-feedback` skill already cover GitHub replies + Slack notification cleanly enough to leave those outside the daemon?
7. Should incoming review requests launch immediately, or wait for CI to become green first?
8. Should re-review trigger on every new head SHA while review is requested, or only when GitHub explicitly re-requests review?
9. Should review worktrees be retained until the PR closes/merges, or cleaned up after each completed review?

## 22. Guiding principles

- GitHub is truth.
- The daemon is deterministic.
- Claude does judgment.
- Orca is an adapter, not a dependency baked into workflow logic.
- Every event is idempotent.
- Resume from observed state, not assumptions.
- No idle LLM.
- Human owns merge.
- Start with one worker and make it boringly reliable before adding parallelism.
