# Work Agent Daemon — PRD

> Vocabulary follows `CONTEXT.md` (Operator, Work Item, Owned Issue, Review Request, Eligible, Workspace, Wake, Wake Reason, Entry Skill, Routing, Held Wake, Settled, Quiet Period, Paused, Tick). Hard-to-reverse decisions are recorded in `docs/adr/`. Prior art: `docs/reference/coding-agent-loop.md` (reference only — no code may be copied).

## 1. Summary

Build a small local daemon (macOS and Windows) — a Go core shipped as a Wails v3 system-tray app plus a headless CLI (ADR-0004) — that watches GitHub on behalf of the Operator, then uses Orca to create or resume an isolated Workspace and Wakes Claude Code in it with a deterministic entry prompt.

The daemon itself contains no LLM and performs no engineering judgment. GitHub is the source of truth for Work Item state. Orca owns local worktrees and terminals. Claude Code owns planning, implementation, review, and feedback handling via the Entry Skill and its Routing.

### Core idea

```text
GitHub
      │
      ▼
local workflow daemon
(no LLM)
      │
      ▼
workflow reducer (pure)
      │
      ├── Eligible issue assigned ─────┐
      │                                │
      ├── review requested on PR ──────┤
      │                                │
      └── PR feedback / CI settled ────┤
                                       ▼
                              Orca (WorkspaceBackend adapter)
                                │
                      create / resume Workspace
                                │
                                ▼
                           Claude Code
                                │
                  Entry Skill → Routing → skills
```

## 2. Goals

1. Remove repetitive manual Orca UI actions.
2. Automatically create a Workspace when an Eligible issue is assigned to the Operator.
3. Wake Claude Code in that Workspace with a deterministic entry prompt.
4. Track issue → Workspace → PR relationships durably.
5. Detect PR review feedback and Settled CI failures.
6. Detect PRs opened by other developers where the Operator is requested as a reviewer.
7. Wake an isolated Claude Code review in a Review Workspace for each Review Request.
8. Re-review when a reviewed PR receives a new head SHA and review is still requested.
9. Resume the correct Workspace and Claude conversation when new work appears.
10. Keep engineering decisions inside Claude Code, not the daemon.
11. Avoid additional model/API usage while idle.
12. Preserve human approval for sensitive actions such as merge, marking PRs ready, and submitting reviews.
13. Be small enough to understand, modify, and run locally on a managed work Mac or a Windows dev machine.

## 3. Non-goals

MVP will not:

- replace Claude Code
- replace any skill library (the daemon is skill-agnostic; see §10)
- replace Orca as the local worktree/session UI
- decide architecture or implementation strategy
- merge PRs automatically
- deploy code
- manage multiple developers
- expose a public webhook server
- depend on a hosted backend
- require an LLM outside Claude Code
- launch the Orca desktop app on its own

## 4. Primary workflows

### A. New Owned Issue

```text
issue assigned to the Operator in an allowlisted repo
        ↓
daemon discovers issue on a Tick
        ↓
Eligible? (carries the Eligibility Label)
        ↓
create Owned Issue Work Item (PENDING_WORKSPACE)
        ↓
create Workspace via Orca (held if Orca is unreachable)
        ↓
Wake: <entrySkill> issue <repo>#<number>
        ↓
IN_PROGRESS — Claude determines next engineering step via Routing
```

### B. PR opened

Claude Code creates a **draft** PR through the existing workflow. The daemon discovers the PR and attaches it to the Work Item:

```text
issue #428
Workspace issue-428
PR #901
state WAITING_FOR_CI
```

### C. Review feedback

```text
new unresolved review feedback from an allowed author
  (write access: OWNER / MEMBER / COLLABORATOR, or a configured bot;
   quoted text and code fences ignored)
        ↓
wait for a submitted review, or a Quiet Period with no new comments
        ↓
Wake (held while the agent is working): <entrySkill> feedback <repo>#<PR>
        ↓
ADDRESSING_FEEDBACK
        ↓
Claude addresses feedback, verifies, pushes (non-force) to its own branch
        ↓
head SHA changes → WAITING_FOR_CI
```

### D. CI failure

```text
all checks for the head SHA Settled, at least one failed
        ↓
dedupe by head SHA (one Wake per head)
        ↓
Wake: <entrySkill> ci-failure <repo>#<PR>
        ↓
ADDRESSING_FEEDBACK
```

Never Wake for repeated observations of the same Settled failure, and never Wake while checks are still running.

### E. Ready to merge

When:

- CI on the head SHA is Settled and green
- no actionable unresolved review feedback remains
- PR is open

set `READY_TO_MERGE` and notify the Operator. Never merge automatically.

### F. Review Request

```text
PR #333 requests the Operator's review
        ↓
daemon discovers review request on a Tick
        ↓
wait for CI on the head SHA to be Settled (pass or fail)
        ↓
create Review Request Work Item + Review Workspace
  (fetch refs/pull/333/head → origin/pr/333;
   new untracked local branch from it — ADR-0003)
        ↓
Wake: <entrySkill> review <repo>#333
        ↓
REVIEWING — Claude reviews and prepares findings
        ↓
Operator approves before anything is submitted
        ↓
REVIEWED
```

Re-review: when the head SHA changes while the review is still requested (or the Operator is explicitly re-requested), wait for a Quiet Period and Settled CI, refresh the pull ref, and Wake the same Review Workspace once for the new head.

If the review request disappears *because the Operator submitted a review*, the Work Item stays `REVIEWED` (an explicit re-request picks it up again). Any other removal, or the PR merging/closing, moves it to `DONE`.

Review Workspaces can never push to the author's branch — enforced structurally by ADR-0003.

## 5. State machine

Work Item state is derived only from GitHub observations plus the daemon's own Wake records (ADR-0001). The daemon does not track engineering phases.

```ts
type OwnedIssueState =
  | "PENDING_WORKSPACE"
  | "IN_PROGRESS"          // Woken, no PR yet
  | "WAITING_FOR_CI"
  | "WAITING_FOR_REVIEW"
  | "ADDRESSING_FEEDBACK"  // Woken for feedback or CI failure, until head SHA changes
  | "READY_TO_MERGE"
  | "DONE"
  | "PAUSED"
  | "FAILED";

type ReviewRequestState =
  | "PENDING_WORKSPACE"
  | "REVIEWING"            // Woken, current head not yet reviewed
  | "REVIEWED"
  | "DONE"
  | "PAUSED"
  | "FAILED";
```

- `PAUSED` — set by `work-agent pause`, or automatically when an Owned Issue stops being Eligible (label removed or reassigned). No Wakes; nothing deleted. Re-adding the label / reassignment resumes it.
- `FAILED` — only for repeated failures of the daemon's *own* actions (e.g. Workspace creation failed N times).
- There is no `BLOCKED` state. Orca's live agent state (`working`, `waiting`, `blocked`, …) is a signal read each Tick to hold Wakes and notify the Operator, never stored.

### Transitions — Owned Issue

```text
PENDING_WORKSPACE → IN_PROGRESS          Workspace created + Woken
IN_PROGRESS       → WAITING_FOR_CI       PR discovered
WAITING_FOR_CI    → ADDRESSING_FEEDBACK  CI Settled with failure (Woken)
WAITING_FOR_CI    → WAITING_FOR_REVIEW   CI Settled green, feedback outstanding / change request standing
WAITING_FOR_CI    → READY_TO_MERGE       CI Settled green, no actionable feedback
WAITING_FOR_REVIEW→ ADDRESSING_FEEDBACK  actionable feedback (Woken)
WAITING_FOR_REVIEW→ READY_TO_MERGE       feedback addressed, no change request standing, CI green
ADDRESSING_FEEDBACK → WAITING_FOR_CI     head SHA changed
READY_TO_MERGE    → WAITING_FOR_CI       head SHA changed
any               → DONE                 PR merged or closed / issue closed
any active        → PAUSED               Operator pause, or no longer Eligible
any active        → FAILED               daemon's own action repeatedly failing
```

### Transitions — Review Request

```text
PENDING_WORKSPACE → REVIEWING   CI Settled, Review Workspace created + Woken
REVIEWING         → REVIEWED    review prepared/submitted per policy
REVIEWED          → REVIEWING   head SHA changed (Quiet Period + Settled CI) or explicit re-request
any               → DONE        request removed (not by the Operator's own review), PR merged/closed
any active        → PAUSED | FAILED
```

## 6. Event model

MVP polls GitHub (default every 45s); no webhooks.

```ts
type WorkflowEvent =
  | { type: "ISSUE_ASSIGNED"; repo: string; issue: number }
  | { type: "ISSUE_INELIGIBLE"; repo: string; issue: number }
  | { type: "PR_DISCOVERED"; repo: string; issue: number; pr: number }
  | { type: "PR_REVIEW_SUBMITTED"; repo: string; pr: number; reviewId: string }
  | { type: "PR_COMMENTS_QUIET"; repo: string; pr: number; marker: string }
  | { type: "REVIEW_REQUESTED"; repo: string; pr: number; headSha: string; requestedBy?: string }
  | { type: "REVIEW_REQUEST_REMOVED"; repo: string; pr: number; byOperatorReview: boolean }
  | { type: "REVIEW_HEAD_CHANGED"; repo: string; pr: number; oldHeadSha: string; headSha: string }
  | { type: "CI_SETTLED"; repo: string; pr: number; headSha: string; failed: boolean }
  | { type: "PR_MERGED"; repo: string; pr: number }
  | { type: "PR_CLOSED"; repo: string; pr: number };
```

Every event has a stable dedupe marker:

- issue assignment: `repo#issue:assigned` (not keyed by `updatedAt`, which changes on every edit to the issue; pausing/resuming on Eligibility changes will need its own marker)
- review request: `repo#pr:review-request:<reviewer>:<headSha>`
- review head change: `repo#pr:review-head:<headSha>`
- submitted review: review ID
- quiet comments batch: highest comment ID in the batch
- CI: `repo#pr:ci:<headSha>` (one per head, once Settled)
- merge: merge commit SHA

## 7. Durable data

MVP uses a local JSON state file (no database).

```text
~/.work-agent/
  config.json
  state.json
  routing.md        # optional Operator-level Routing override (read by the Entry Skill)
  logs/
```

Move to SQLite later if concurrent writes/history become useful.

### Work Item record

```ts
type WorkItem = OwnedIssueWorkItem | ReviewRequestWorkItem;

interface Workspace {
  orcaIdentityKey: string;   // Orca identity.key (stable)
  path: string;
  branch: string;
  claudeSessionId: string;   // generated by the daemon — ADR-0002
}

interface BaseWorkItem {
  id: string;
  repo: string;
  workspace?: Workspace;

  processedEventIds: string[];
  heldWake?: { reason: WakeReason; since: string; why: "agent-working" | "backend-unavailable" | "quiet-period" | "capacity" };

  createdAt: string;
  updatedAt: string;
  lastWakeAt?: string;
  lastError?: string;
  consecutiveActionFailures: number;
}

interface OwnedIssueWorkItem extends BaseWorkItem {
  kind: "OWNED_ISSUE";
  state: OwnedIssueState;
  issue: number;
  issueUrl: string;
  pr?: number;
  prUrl?: string;
  headSha?: string;
}

interface ReviewRequestWorkItem extends BaseWorkItem {
  kind: "REVIEW_REQUEST";
  state: ReviewRequestState;
  pr: number;
  prUrl: string;
  requestedBy?: string;
  headSha: string;
  lastReviewedHeadSha?: string;
}

type WakeReason = "issue" | "feedback" | "ci-failure" | "review";
```

Terminal handles are never persisted — they are re-resolved every Tick.

Atomic state writes: write `state.json.tmp`, optionally `fsync`, rename over `state.json`.

## 8. Configuration

```json
{
  "github": {
    "account": "dwatts1772",
    "repos": ["org/project-a"],
    "eligibilityLabel": "agent-ready",
    "pollIntervalSeconds": 45,
    "quietPeriodMinutes": 5,
    "feedbackBots": ["coderabbitai[bot]"]
  },
  "orca": {
    "enabled": true
  },
  "claude": {
    "entrySkill": "/work-item"
  },
  "capacity": {
    "ownedIssueSlots": 1,
    "reviewRequestSlots": 1
  },
  "notify": {
    "desktop": true
  },
  "automation": {
    "autoCreateWorktree": true,
    "autoCreateReviewWorktree": true,
    "autoWakeOnReview": true,
    "autoWakeOnReviewRequest": true,
    "autoWakeOnReviewHeadChange": true,
    "autoWakeOnCiFailure": true
  }
}
```

- `github.account` is the Operator. Every `gh` child process runs with `GH_TOKEN` from `gh auth token --user <account>`; at startup the daemon verifies `gh api user` returns that login and refuses to run otherwise. The daemon never relies on `gh`'s active account or `@me`.
- `eligibilityLabel` is required in MVP (configurable name; can later be made optional per repo).
- There is no `autoMerge` setting — merging is impossible by construction.
- Review feedback only Wakes Claude when its author has write access (OWNER / MEMBER / COLLABORATOR) or is listed in `feedbackBots`; drive-by comments are ignored.

Future configuration: repo-specific settings, quiet hours, Slack, approval policy, smart capacity (§11).

## 9. Integrations

### GitHub

Use the `gh` CLI (JSON output, parsed locally), always as the Operator (§8).

Responsibilities:

- list issues assigned to the Operator in allowlisted repos, with labels
- find linked PRs
- list open PRs where the Operator is requested as a reviewer
- fetch PR head SHA, reviews, review requests, comments, and whether a request was cleared by the Operator's own review
- inspect check runs and determine when a head SHA's CI is Settled
- detect merged/closed PRs

### Orca

Facts (Orca 1.4.219, verified from the local install):

- Requires a running Orca runtime (desktop app, or headless `orca serve`); `orca status --json` reports reachability and `capabilities[]`.
- Invoke `orca.exe` / `orca` directly with argument arrays — on Windows **not** `orca.cmd`, which refuses to forward message bodies.
- Worktrees have a stable `identity.key`; terminal handles are runtime-scoped and go stale after an Orca restart.
- `worktree create` always creates a new branch; `--base-branch origin/<ref>` sets its start point. The CLI cannot check out an existing branch or PR head.
- `worktree create --agent claude --prompt` starts Claude interactively with the prompt as argv.
- `terminal create --worktree <sel> --command "<cmd>"` runs a command in an existing Workspace; `terminal send --text … --enter` (idempotent via `--retry-request`) sends into a live session.
- `worktree ps --json` reports per-agent state (`working`, `waiting`, `blocked`, `done`, `idle`) via Orca's Claude hooks.
- Orca does not expose Claude session IDs or Claude plan usage through the CLI.

Wrap all Orca interaction behind an adapter:

```ts
interface WorkspaceBackend {
  available(): Promise<boolean>;
  createForIssue(input: CreateWorkspaceInput): Promise<Workspace>;
  createForReview(input: CreateReviewWorkspaceInput): Promise<Workspace>;
  agentState(workspace: Workspace): Promise<"working" | "waiting" | "idle" | "none">;
  wake(workspace: Workspace, prompt: string): Promise<void>;
  exists(workspace: Workspace): Promise<boolean>;
}
```

**Wake semantics (ADR-0002):** the first Wake starts Claude with `--session-id <daemon uuid>`. Later Wakes resume that conversation — `terminal send` into a live idle session, otherwise `claude --resume <uuid> "<prompt>"` in a new terminal — falling back to a fresh session if resume fails. A Wake is Held while the agent is `working`.

**Review Workspaces (ADR-0003):** fetch `+refs/pull/<n>/head:refs/remotes/origin/pr/<n>`, then `worktree create --name review-pr-<n> --base-branch origin/pr/<n>`. On head change, re-fetch; the Entry Skill moves the Workspace to the new head.

**Orca unavailable:** keep observing GitHub, record events, Hold all Workspace actions, retry next Tick, notify the Operator. Never launch Orca automatically.

### Claude Code

The daemon never encodes workflow logic. Every Wake is one prompt:

```text
<entrySkill> issue org/project-a#428
<entrySkill> feedback org/project-a#901
<entrySkill> ci-failure org/project-a#901
<entrySkill> review org/project-a#333
```

### Notifications

A `Notifier` interface in the core; MVP ships console + JSONL log (always on) and native desktop notifications delivered by the Wails v3 tray app (Windows toast / macOS Notification Center). Slack plugs into the same interface in phase 2.

Notify on: Orca unavailable, Work Item Paused, `READY_TO_MERGE`, review findings ready for approval, agent waiting on the Operator, Work Item `FAILED`.

## 10. Entry Skill (`/work-item`) and Routing

The Entry Skill lives in this repo (e.g. `skills/work-item/`), shipped as a Claude Code plugin or linked into `~/.claude/skills`, so the daemon and the Wake prompt contract change together.

Responsibilities:

1. Inspect the supplied issue/PR and the Workspace's git state.
2. Resolve Routing and follow it.
3. Never duplicate the implementation of the skills it routes to.

**Routing is layered markdown**, read by Claude, never by the daemon:

1. built-in default (targets mattpocock-skills) shipped with the Entry Skill
2. Operator override: `~/.work-agent/routing.md`
3. repo override: `docs/agents/work-item-routing.md` in the target repo

Operators on a different workflow can override Routing, or point `claude.entrySkill` at their own skill entirely.

Default Routing (mattpocock-skills):

```text
issue + unclear requirements           → grill-with-docs
issue + large feature                  → grill-with-docs → to-spec → to-tickets
issue + implementation ready           → implement
feedback                               → address feedback, verify, push (non-force)
ci-failure                             → diagnose, fix, verify, push (non-force)
review                                 → code-review PR#<n>; findings held for Operator approval
review + new head                      → code-review against latest head, focus on new diff + prior findings
```

**Push policy:** in Owned Issue Workspaces Claude may push (never force) to its own branch and open **draft** PRs; marking ready-for-review requires the Operator. Review Workspaces never push.

## 11. Concurrency

The `CapacityPolicy` seam decides how many Wakes may proceed:

```ts
interface CapacityPolicy {
  freeSlots(kind: WorkItem["kind"], working: AgentSnapshot[]): number;
}
```

MVP policy: fixed slots — `ownedIssueSlots: 1`, `reviewRequestSlots: 1`. A slot is occupied only by an agent Orca reports as `working` (idle or waiting-on-Operator agents free their slot). Held Wakes are released FIFO, Review Requests first.

Never Wake the same Workspace twice concurrently: in-memory lock plus persisted `lastWakeAt` / state guard.

Phase 2: a smart policy using RAM, CPU (sampled — there is no load average on Windows), and Claude plan usage (session / weekly limits) so an unattended daemon cannot burn the Operator's weekly limit. See the research item in §19.

## 12. Safety

Required safeguards:

- repo allowlist
- Operator identity pinned by config and verified at startup
- only Owned Issues that are Eligible are worked
- only PRs where the Operator is explicitly requested as reviewer are reviewed
- Review Workspaces structurally cannot push to the author's branch (ADR-0003)
- never merge, force push, or deploy — no code path exists
- never mark a PR ready or submit a review without the Operator
- never delete branches/worktrees automatically in MVP
- never send customer/public messages
- log every external action; redact secrets (including `GH_TOKEN`) from logs
- invoke only known binaries with argument arrays; no shell interpolation
- daemon runs as the logged-in developer, not root/admin

Approval boundary:

```text
Automatic:
- create Workspace
- Wake Claude
- run tests
- inspect GitHub
- push (non-force) to the Owned Issue's own branch
- open draft PR

Operator gate:
- mark PR ready for review
- reply to GitHub review (per Routing policy)
- submit a review on someone else's PR
- merge (always, outside the daemon)
```

## 13. Tray app and CLI

**Tray app** (Wails v3; frontend Svelte + TypeScript + Tailwind + shadcn-svelte) — runs the Tick loop in the background, single instance, starts at login:

- tray icon reflecting overall status (ok / Held Wakes / Orca unavailable / needs Operator)
- tray menu: open status window, pause all, Tick now, quit
- status window: Work Items with state, Workspace, PR, Held Wake reason; per-item pause / resume / wake; DONE Review Workspaces marked safe to clean up
- native notifications (§9)

**Headless CLI** (`work-agent`) — same core, no Wails:

```bash
work-agent tick [--dry-run]   # one Tick, then exit — primary dev/debug command;
                              # --dry-run observes GitHub and prints reducer actions,
                              # touching neither Orca nor Claude nor state
work-agent list
work-agent inspect org/project-a#428
work-agent pause org/project-a#428
work-agent resume org/project-a#428
work-agent wake org/project-a#428
```

The CLI and the tray app must not run Ticks concurrently (shared lock on the state directory).

## 14. Internal modules

Go module; type shapes elsewhere in this PRD are written in TypeScript notation for brevity.

```text
cmd/
  work-agent/        # headless CLI entrypoint
  work-agent-tray/   # Wails v3 tray app entrypoint (+ frontend/)

internal/
  config/
  state/             # JSON store, atomic writes, state-dir lock
  github/            # gh runner pinned to the Operator; discovery; events
  workspace/         # WorkspaceBackend interface + Orca adapter
  workflow/          # pure reducer, reconcile, handlers, capacity (CapacityPolicy)
  process/           # argv-only runner, absolute binary resolution, child-process cleanup
  notify/            # Notifier interface, console/JSONL
  core/              # Tick loop wiring; embedded by both entrypoints

skills/
  work-item/         # Entry Skill + default Routing
```

No package under `internal/` imports Wails.

### Important rule

The workflow reducer is pure:

```ts
const actions = reduce(currentState, event);
// e.g. [{ type: "CREATE_WORKSPACE" }, { type: "WAKE", reason: "issue" }]
```

Handlers execute actions. The orchestration logic is testable without GitHub, Orca, or Claude.

## 15. Reconciliation model

Do not rely only on edge-triggered events. Every Tick reconciles observed GitHub truth against persisted state (e.g. state says `WAITING_FOR_REVIEW`, GitHub says merged → `DONE`). Held Wakes are re-evaluated every Tick. This gives recovery after daemon restart, sleep, network outage, missed Tick, or Orca restart.

## 16. Logging

Structured JSONL plus a human-readable console formatter.

```json
{"time":"...","level":"info","item":"org/project-a#428","event":"PR_REVIEW_SUBMITTED","action":"WAKE","reason":"feedback"}
```

## 17. Startup

The tray app starts at login (macOS login item, Windows startup registration), toggled from the tray menu. There is no separate service supervisor (ADR-0004): if the app crashes it is not auto-restarted; reconciliation makes a manual restart safe.

Login items and startup entries get a minimal `PATH`, so `gh`, `git`, `orca` and `claude` are resolved to absolute paths at startup (configurable overrides) and git runs with `GIT_TERMINAL_PROMPT=0`.

The core is OS-agnostic; startup registration and binary resolution are the only per-platform pieces.

## 18. MVP acceptance criteria

1. `work-agent tick` discovers a newly assigned, Eligible issue from an allowlisted repo; an assigned issue without the Eligibility Label is ignored.
2. It creates exactly one Workspace for that issue.
3. Re-running the Tick does not create duplicates.
4. Claude is Woken with `<entrySkill> issue <repo>#<issue>` and a daemon-owned session ID.
5. Once a PR exists, the daemon associates it with the Work Item.
6. A submitted review (or quiet comment batch) Wakes the same Workspace exactly once, resuming the same Claude conversation.
7. Re-observing the same review/comments does nothing.
8. Settled CI failure Wakes the same Workspace exactly once per head SHA; running checks never Wake.
9. Restarting the daemon preserves all associations, Held Wakes, and dedupe state.
10. A merged PR transitions the Work Item to `DONE` without Waking Claude.
11. A PR from another developer requesting the Operator's review creates exactly one Review Request and Review Workspace for that head SHA, after CI is Settled.
12. Claude is Woken in that Review Workspace with `<entrySkill> review <repo>#<pr>`, which routes to `/code-review PR#<pr>` by default.
13. Re-observing the same review request/head SHA does not create or Wake another session.
14. If the head SHA changes while review is still requested, the existing Review Workspace Wakes exactly once for the new SHA (after the Quiet Period).
15. Review Workspaces cannot push to the PR author's branch.
16. Review submission remains Operator-gated.
17. No code path can merge, force push, or deploy.
18. Removing the Eligibility Label pauses the Work Item; re-adding resumes it.
19. With Orca closed, a Tick records GitHub events, Holds Workspace actions, notifies, and completes them on the first Tick after Orca returns.
20. A Wake is Held while the Workspace's agent is `working`.
21. The daemon refuses to start if `gh` cannot act as the configured Operator account.

## 19. Implementation plan

### Setup

- scaffold Go module (headless CLI first; Wails tray app wraps the core later)
- config loader (incl. Operator verification)
- argv-only process runner with absolute binary resolution
- stub-binary test harness (fake `gh` / `orca` / `claude`)
- `state.json` store
- `work-agent tick [--dry-run]`

### Core loop

- Eligible assigned issue discovery via `gh`
- pure workflow reducer + event dedupe
- console + JSONL logging

### Workspaces

- Orca adapter (availability, create, agent state, wake)
- create Workspace from issue; Wake with daemon-owned session ID
- verify idempotency; Held Wakes when Orca is down / agent working

### PR lifecycle

- PR discovery/linking
- submitted review + Quiet Period comment detection
- Settled CI detection
- Wake existing Workspace (resume)
- Review Request discovery, pull-ref fetch, Review Workspace flow
- head-SHA change re-review

### Hardening

- capacity slots
- Wails v3 tray app: Tick loop, status window, desktop notifications
- restart/recovery testing
- start at login (macOS + Windows)
- safety guards
- Entry Skill + default Routing
- README
- dogfood on one real low-risk issue

### Research (ticket)

- **Claude plan usage source.** Orca's UI shows Claude session/weekly/Fable usage; its main process obtains this from `https://api.anthropic.com/api/oauth/usage` (fields `five_hour`, `seven_day`, Fable weekly) using the Claude OAuth token. It is not exposed through the Orca CLI. Investigate: can Orca expose it (CLI, `orca status`, runtime RPC — or a feature request), or is calling that endpoint directly acceptable given it is undocumented? Output feeds the phase-2 smart `CapacityPolicy`.

## 20. Phase 2

- Slack notifier
- smart `CapacityPolicy` (RAM, CPU, Claude plan usage)
- more concurrent Work Items
- SQLite event history
- richer tray UI (history, approvals)
- crash auto-restart (watchdog or OS service)
- stale/DONE Workspace cleanup
- quiet hours
- pause/resume from Slack
- GitHub webhooks instead of polling
- per-repo policies (optional Eligibility Label, etc.)
- optional `orca open` when Orca is unavailable
- optional automatic review submission for trusted repos

## 21. Decisions

Resolved during design (previously open questions):

1. **Workspace identity** — Orca `identity.key` + path + daemon-owned Claude session ID; terminal handles re-resolved each Tick (ADR-0002).
2. **Launch gate** — Eligibility Label required in MVP.
3. **Push** — Claude may push (non-force) to its own branch and open draft PRs; marking ready is Operator-gated. Review Workspaces never push.
4. **CI Wakes** — only once all checks for a head SHA are Settled; one Wake per head.
5. **Feedback batching** — Wake on a submitted review, or after a Quiet Period (default 5 min) of standalone comments.
6. **Replies / notifications** — owned by Routing and the `Notifier` adapter, not the daemon's workflow logic.
7. **Review timing** — Review Requests wait for Settled CI (pass or fail).
8. **Re-review trigger** — new head SHA while requested (after Quiet Period + Settled CI), or explicit re-request; a request cleared by the Operator's own review leaves the item `REVIEWED`.
9. **Review Workspace retention** — retained until the PR merges/closes; listed as safe to clean up; automatic cleanup is phase 2.
10. **State model** — GitHub-derived only; no `PLANNING`/`IMPLEMENTING`/`BLOCKED` (ADR-0001).
11. **Review checkout** — new local branch from fetched `refs/pull/<n>/head` (ADR-0003).
12. **Operator identity** — pinned via config, verified at startup.
13. **Platforms** — macOS and Windows; only startup registration and binary resolution are per-platform.
14. **Orca unavailable** — Hold actions, retry, notify; never auto-launch.
15. **Concurrency** — `CapacityPolicy` seam; MVP fixed slots (1 Owned Issue + 1 Review Request), counting only `working` agents, Review Requests first.
16. **Language and shell** — Go core, Wails v3 tray app + headless CLI, start at login (ADR-0004).
17. **Feedback authors** — only write-access reviewers or configured bots can trigger a feedback Wake.
18. **Prior art** — `coding-agent-loop` is a design reference only; it is unlicensed, so no code is copied.
19. **No approval for `READY_TO_MERGE`** — green, Settled CI with no actionable feedback and no standing change request is `READY_TO_MERGE`; no approval is required, even where branch protection requires reviews. The PR is a draft only the Operator marks ready, so `READY_TO_MERGE` is the hand-off to the Operator, not a merge; GitHub enforces branch protection at merge (#37).

## 22. Guiding principles

- GitHub is truth.
- The daemon is deterministic.
- Claude does judgment.
- Orca is an adapter, not a dependency baked into workflow logic.
- Every event is idempotent.
- Resume from observed state, not assumptions.
- No idle LLM.
- Human owns merge.
- Start with one worker per kind and make it boringly reliable before adding parallelism.
