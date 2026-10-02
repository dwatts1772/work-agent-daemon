# Work Agent Daemon

A local, LLM-free daemon that watches GitHub on behalf of one developer and hands eligible work to Claude Code in isolated workspaces. GitHub is the source of truth; the daemon orchestrates, Claude does the engineering judgment.

## Language

### People and work

**Operator**:
The single developer the daemon acts for, identified by one explicitly configured GitHub account.
_Avoid_: me, user, current user

**Work Item**:
One tracked unit of work the daemon is responsible for — either an Owned Issue or a Review Request.
_Avoid_: workflow record, job, task

**Owned Issue**:
A Work Item for an Eligible GitHub issue assigned to the Operator.
_Avoid_: assigned issue, ticket

**Review Request**:
A Work Item for another developer's pull request on which the Operator is explicitly requested as a reviewer.
_Avoid_: incoming PR, external PR

**Eligible**:
An assigned issue that carries the Eligibility Label in an allowlisted repo; only Eligible issues become Owned Issues.
_Avoid_: ready, actionable

**Eligibility Label**:
The GitHub label that marks an assigned issue as approved for agent work.
_Avoid_: trigger label

### Execution

**Workspace**:
The isolated checkout where Claude works on exactly one Work Item.
_Avoid_: worktree, session (as names for the concept)

**Wake**:
The daemon handing a Work Item's Workspace to Claude with a Wake Reason.
_Avoid_: launch, resume, start, trigger

**Wake Reason**:
Why a Wake happened — `issue`, `feedback`, `ci-failure`, or `review`.

**Entry Skill**:
The single Claude skill every Wake invokes, which decides how to handle the Wake Reason.
_Avoid_: router, dispatcher

**Routing**:
The Entry Skill's mapping from Wake Reason and Work Item shape to the workflow Claude follows; layered as built-in default, Operator override, repo override. The daemon never sees it.
_Avoid_: dispatch rules, skill config

**Paused**:
A Work Item the daemon will not Wake — set by the Operator, or automatically when an Owned Issue stops being Eligible. Nothing is deleted while Paused.
_Avoid_: blocked, suspended

**Held Wake**:
A Wake the daemon has decided on but deferred — because the agent is working, the Workspace's backend is unavailable, or a Quiet Period has not elapsed. It is retried on a later Tick, never dropped.
_Avoid_: queued wake, pending wake

**Settled**:
The CI state of a head commit once every check for it has concluded, pass or fail.
_Avoid_: finished, done (for CI)

**Quiet Period**:
How long a pull request must go without new review comments or pushes before the daemon acts on them.
_Avoid_: debounce window

**Tick**:
One cycle of observing GitHub and reconciling Work Items against what was observed.
_Avoid_: poll, cycle, run
