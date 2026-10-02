# Reference: ableinc/coding-agent-loop

<https://github.com/ableinc/coding-agent-loop> — a Go daemon that picks up `agent-ready` GitHub issues, runs headless Claude Code in a git worktree, and opens draft PRs. It is where this project's idea originated.

**Reference only — do not copy code.** The repo has no license (all rights reserved). Study the designs below and reimplement them in our own code. If copying ever becomes desirable, ask the author to add a license first.

## How it differs from us

| Them | Us |
|---|---|
| headless `claude -p`, `--no-session-persistence`, `--disable-slash-commands`, `bypassPermissions` | interactive Claude Woken in an Orca Workspace; daemon-owned resumable session (ADR-0002); `/work-item` is a slash command, so slash commands must stay enabled |
| the harness does all commits, pushes, PRs, comments | Claude (via Routing) does engineering writes; the daemon is LLM-free and GitHub-read-mostly |
| discovery by label only | Owned Issues need assignee **and** Eligibility Label; plus Review Requests |
| runs tests locally (`verify`) | relies on Settled CI per head SHA |
| feedback = `@coding-agent` mentions | feedback = submitted reviews / Quiet Period comments from allowed authors |
| SQLite leases, Linux/systemd, plain git worktrees | JSON state, macOS+Windows tray app (ADR-0004), Orca |

## Vocabulary map

| Their term | Ours |
|---|---|
| trigger label `agent-ready` | Eligibility Label |
| phase (plan/wait/implement/done) | none — Work Item state is GitHub-derived (ADR-0001); Wake Reason picks the workflow |
| claim / lease | capacity slot + per-Workspace lock |
| gate / deferred | Held Wake |
| run | one Wake |
| adopt | reconciliation of an existing PR |
| workspace / worktree | Workspace |
| harness | the daemon |
| owners / exclude_repos | repo allowlist |

## Designs worth reimplementing

- **Pure phase derivation + table-driven tests** — `internal/orchestrator/phase.go` (`decidePhase`), `phase_test.go`. Model for our reducer tests.
- **PR ↔ issue matching** — `internal/gh/gh.go` `prCoversIssue`, `matchesIssueBranch`, `referencesIssue` (careful `#4` vs `#42`).
- **Search with defensive post-filter** — `gh.go` `SearchIssues` re-checks owner/repo after `gh search`.
- **Feedback author filtering** — `internal/orchestrator/prcomments.go` `authorAllowed` (OWNER/MEMBER/COLLABORATOR), `lineMentions` (ignore quoted text and code fences).
- **Usage-limit / auth-expiry detection** — `internal/gate/gate.go` `DetectLimit`, `DetectAuthExpired`: limit phrases, reset-epoch parsing with a 24h sanity cap; a limit hit is "deferred", not a failure (≈ our Held Wake).
- **Minimal-PATH robustness** — `internal/git/workspace.go` `credentialArgs`: resolve `gh`/`git` by absolute path and set `GIT_TERMINAL_PROMPT=0`; services/login items get a minimal PATH.
- **Retry back-off** — `internal/orchestrator/loop.go` `retryDelay`.
- **Testing with stub binaries** — `adopt_test.go` `stubGH`: fake `gh`/`claude` executables on disk; a dry-run test asserting nothing is spent or mutated.
- **`--once` / `--dry-run`** — our `work-agent tick [--dry-run]`.
- **README "Troubleshooting" / "Verification"** — practical service-PATH and toolchain lessons.

## Deliberately not adopted (v1)

Hidden HTML comment markers (our daemon writes no comments), plan→`implement` approval comment, model ladders/cooldowns, web control API, Discord.
