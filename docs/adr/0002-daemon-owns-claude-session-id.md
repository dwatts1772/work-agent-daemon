# The daemon owns each Workspace's Claude session ID

Orca does not expose the Claude session IDs it tracks, and its terminal handles do not survive an Orca restart. So the daemon generates a UUID per Workspace, starts Claude with `claude --session-id <uuid>`, and later Wakes resume that conversation (`terminal send` into a live idle session, otherwise `claude --resume <uuid>` in a new terminal), falling back to a fresh session if resume fails. A Workspace is identified durably by Orca's `identity.key` plus path plus this session ID; terminal handles are re-resolved every Tick.

## Considered Options

- Fresh session per Wake, rebuilding context from GitHub — rejected as the default because the implementation context is valuable when addressing feedback or CI failures.
- Reading Orca's internal `providerSessionId` — not exposed by any CLI command; would depend on private internals.
