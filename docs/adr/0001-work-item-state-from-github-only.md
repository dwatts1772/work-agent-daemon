# Work Item state is derived only from GitHub

A Work Item's state is computed from what GitHub shows (PR existence, CI status on the head commit, unresolved review feedback, review requests, merged/closed) plus the daemon's own record of Wakes — never from the engineering phase Claude is in. We dropped the PRD's `PLANNING` / `IMPLEMENTING` / `STARTING` / `BLOCKED` states because nothing observable distinguishes them; tracking them would need Claude to report back to the daemon, turning a one-way handoff into a two-way protocol. Keeping state GitHub-derived means any Work Item can be rebuilt from scratch after a restart, sleep, or missed Tick.

## Consequences

- Orca's live agent state (`working`, `waiting`, `blocked`, …) is read each Tick as a *signal* — to hold Wakes and notify the Operator — and is never stored as Work Item state.
- "Claude is stuck waiting for me" is a notification, not a state.
