# Research: Claude plan usage source for the phase-2 CapacityPolicy

Date: 2026-10-02 · Issue #16 · Orca 1.4.219, Claude Code 2.1.288, Windows 11

## Question

How can the local, LLM-free daemon read the Operator's Claude plan usage (5-hour session, 7-day weekly, Fable weekly) each Tick, so a phase-2 `CapacityPolicy` (`internal/workflow/capacity.go`, `FreeSlots(kind, agents) int`) can Hold Wakes before an unattended daemon burns the Operator's weekly limit?

## TL;DR

**Orca already exposes plan usage through its CLI.** The PRD statement in §9 ("Orca does not expose … Claude plan usage through the CLI") is **wrong for 1.4.219**. `orca.exe account list --json` returns the runtime's cached `rateLimits` snapshot, including `rateLimits.claude.session` / `.weekly` / `.fableWeekly` with `usedPercent` and `resetsAt`. The field is undocumented: it is missing from `agent-context`, and the human output does not show it.

Recommendation for phase 2 is layered:

1. **Proactive, primary:** each Tick, read `orca.exe account list --json` and use `result.rateLimits.claude`. When any window is over a configured threshold, or the snapshot is stale or in error, `FreeSlots` returns 0 and new Wakes become Held Wakes (why: `capacity`). The daemon never touches the OAuth token, makes no extra network call, and does nothing extra while idle. **Prerequisite:** Orca's cache goes stale while it is unfocused, so phase 2 needs a live feed first (the Orca feature request, or Orca's statusline feed) — see Recommendation.
2. **Reactive, backstop:** read the agent's terminal with `orca terminal read` only when Orca reports the Workspace's agent as `done` with `mainAgent.outcome = failure`, and match Claude's limit message to get a reset time. Hold until then.
3. **Do not** read `~/.claude/.credentials.json` or call `api.anthropic.com/api/oauth/usage` from the daemon.
4. File the drafted Orca feature request below so the field becomes a documented, versioned contract.

---

## Candidate 1: Orca runtime `accounts.list` (via `orca account list --json`)

**Evidence**

- CLI: `orca.exe account list --json` (run 2026-10-02) returned the following. Emails and IDs are redacted.
  ```json
  { "ok": true, "result": {
      "claude": { "accounts": [], "activeAccountId": null, ... },
      "codex":  { ... },
      "rateLimits": {
        "claude": {
          "provider": "claude",
          "session":     { "usedPercent": 30, "windowMinutes": 300,   "resetsAt": 1791016800212, "resetDescription": "Sat 1:40 AM" },
          "weekly":      { "usedPercent": 6,  "windowMinutes": 10080, "resetsAt": 1791460800212, "resetDescription": "Thu 5:00 AM" },
          "fableWeekly": { "usedPercent": 0,  "windowMinutes": 10080, "resetsAt": 1791460800000, "resetDescription": "Thu 5:00 AM" },
          "updatedAt": 1791007266412, "error": null, "status": "ok",
          "usageMetadata": { "source": "oauth", "attemptedSources": ["oauth"],
                             "credentialSource": "credentials-file", "authProvenance": "system",
                             "lastSuccessfulSource": "oauth" } },
        "codex": { ... }, "gemini": { "status": "unavailable", ... }, ... } } }
  ```
  `resetsAt` and `updatedAt` are epoch **milliseconds**. `usedPercent` is clamped to 0–100.
- CLI handler, `resources/app.asar.unpacked/out/cli/handlers/account.js` (lines 224–227): `'account list'` calls runtime RPC **`accounts.list`** with `{ refreshUsage: false }`. Its comment reads: *"this command renders no usage numbers, so skip the forced provider refresh — it is one serial network round-trip per managed account."* The human formatter prints only accounts. The `--json` output is the raw snapshot, so `rateLimits` appears only there.
- Runtime RPC, `out/main/index.js` (minified): `J({name:'accounts.list', params:{refreshUsage: boolean default true}, handler: … (e.refreshUsage && await t.refreshAccountsForMobile(), t.getAccountsSnapshot())})`. `getSnapshot()` returns `{claude, codex, rateLimits: rateLimitsService.getState()}`. Related RPCs in the same table are `accounts.subscribe` (streams `{type:'snapshot'}` on change), `accounts.unsubscribe`, `accounts.selectClaude`, and others. `refreshUsage: true` forces a live fetch through `refreshForMobile()`. The daemon should **not** use it, because it spends the endpoint's budget (see Candidate 2).
- Desktop IPC (renderer↔main, not reachable from the CLI): `rateLimits:get`, `rateLimits:refresh`, `rateLimits:setPollingInterval`, `rateLimits:refreshClaudeForTarget`, and others (`out/main/index.js`).
- How Orca fills the cache, from `out/main/index.js`:
  - It polls on a timer every `pollInterval`, default `Tsa = 900*1e3` (15 min) with a minimum of `Esa = 30*1e3`. The timer **only fetches when the Orca window is visible, not minimised, and focused** (`shouldBackgroundPoll()`). There is also a deferred startup refresh of 1 s.
  - It also takes a live feed from the Claude statusline. `ingestLiveClaudeRateLimits` updates `session`/`weekly` from `rate_limits` posted to Orca's local hook server at `/statusline/claude` (`out/shared/claude-statusline-rate-limits.js`). Its comment says this feed costs no usage-endpoint budget *"(the endpoint 429s under Orca's polling)"*. Orca installs its managed statusline only when the user has none: `out/main/chunks/managed-agent-hook-controls-*.js` leaves a `user` statusLine alone. This Operator has their own statusLine (`~/.claude/settings.json` → a personal Python script), so the live feed is inactive here and the snapshot above came from `source: "oauth"`.
- The capability list from `orca status --json` has no usage or rate-limit capability. `orca agent-context --json` documents `account list`, but nothing about `rateLimits`.

**Stability:** Undocumented. The JSON-RPC method `accounts.list` is versioned only by Orca's general protocol, and `rateLimits` is an incidental field of the snapshot. The shape is internal and could change in any Orca release. Orca is shipped often (1.4.x). Mitigation: parse defensively, and treat a missing field, `status != "ok"`, `error != null`, or a stale `updatedAt` as "unknown", which degrades capacity (see Recommendation).

**Freshness caveat:** With Orca minimised or unfocused and no managed statusline, the snapshot can be hours old. `updatedAt` is the signal to check. On this machine it was about 12 min old at first read, and a second read later in the session returned the identical `updatedAt` — the cache had not refreshed in between.

**Auth implications:** The daemon handles no credential. Orca reads the token itself, first from the keychain and then from `~/.claude/.credentials.json`. For managed accounts it can refresh the token and write it back to that file. Whatever ToS position applies to Orca reading the token is Orca's and the Operator's choice of tooling, not the daemon's.

**Gives:** Proactive % used and reset time for 5h, 7d, and **Fable weekly**. The Fable window comes from `limits[]` entries with `kind === "weekly_scoped"` and `scope.model.display_name == "fable"`, falling back to `fable_weekly` / `fable_seven_day` / `seven_day_fable`.

## Candidate 2: Direct call to `GET https://api.anthropic.com/api/oauth/usage`

**Evidence**

- Orca bundle `out/main/index.js`: the constant is `https://api.anthropic.com/api/oauth/usage` with a 10 s timeout. It is called with headers `Authorization: Bearer <token>`, `anthropic-beta: oauth-2025-04-20`, and **`User-Agent: claude-code/2.1.0`**, so Orca presents itself as Claude Code. Response fields it reads: `five_hour`, `seven_day` (`utilization` or `used_percentage`, `resets_at`), `limits[]`, `fable_weekly`, `fable_seven_day`, `seven_day_fable`. It handles HTTP 429 as `rate-limited` (with `retryAfterMs`) and 401 as `stale-token`.
- Token source in the bundle: the keychain first, then `<CLAUDE_CONFIG_DIR or ~/.claude>/.credentials.json`. Locally, `~/.claude/.credentials.json` exists with top-level keys `mcpOAuth` and `claudeAiOauth`. `claudeAiOauth` has keys `accessToken`, `refreshToken`, `expiresAt`, `refreshTokenExpiresAt`, `scopes`, `subscriptionType`, and `rateLimitTier`. Values were not read.
- Not documented by Anthropic. I found no official doc page for it. Community issues refer to it: anthropics/claude-code#31021 (persistent 429s from the endpoint, closed), #34346, and #79022, which lists `seven_day_opus`, `seven_day_sonnet`, and `limits[]` `weekly_scoped` entries.
- The Operator's own statusline script (a personal dotfile, path omitted) already calls this endpoint with a file cache — evidence the endpoint is in informal use, not that it is sanctioned.

**Stability:** Undocumented private endpoint, with a beta header pinned to `oauth-2025-04-20`. It is rate-limited aggressively enough that Orca abandoned frequent polling for it (comment above). Field names have already drifted: Orca tolerates `utilization`/`used_percentage` and several Fable keys.

**Auth implications (decisive):** Claude Code's Legal and compliance page (code.claude.com/docs/en/legal-and-compliance, "Authentication and credential use") says OAuth is *"intended exclusively for purchasers of Claude Free, Pro, Max, Team, and Enterprise subscription plans and is designed to support ordinary use of Claude Code and other native Anthropic applications"*, and *"developers may not collect, store, or intermediate Claude.ai credentials or session tokens"*. A daemon reading `.credentials.json` and replaying the bearer token against an Anthropic endpoint is exactly that kind of third-party token use. It would also have to handle refresh: either race Claude Code's own refresh-token rotation, or call refresh itself and write the file, which Orca does with care around live sessions ("waiting for the live Claude terminal to rotate its credentials"). Not acceptable for this project.

**Gives:** Proactive %, reset times, and per-model weekly windows. This is the most complete data, but it carries ToS and breakage risk.

## Candidate 3: Claude Code statusline `rate_limits` (official)

**Evidence:** code.claude.com/docs/en/statusline documents `rate_limits.five_hour.used_percentage` and `rate_limits.seven_day.used_percentage` (0–100), plus `.resets_at` (Unix epoch seconds). These are *"only present for claude.ai Pro and Max subscribers … and only after the first API response in the session"*, and each window *"may be independently absent"*. The statusline script runs on each assistant message, after `/compact`, when a `resets_at` passes, and on a `refreshInterval`, debounced at 300 ms. Per-model (Fable) weekly windows are **not** passed (anthropics/claude-code#79022, closed). Orca's own source notes it requires Claude Code ≥ 2.1.80.

**Stability:** Documented and supported. This is the most stable source.

**Auth implications:** None. Claude Code supplies the data. The data comes from response headers that Claude Code already receives, so it costs no extra model or API usage.

**Gives:** Proactive 5h/7d % and reset times, but **no Fable window**. It updates only while some Claude session is active. The data is per-statusline invocation, so the daemon would need a sink: the Operator's statusline script would write `rate_limits` to a file the daemon reads. There is only one `statusLine` slot, and it currently belongs to the Operator's script (or to Orca's managed one, which posts to Orca, so Candidate 1 already benefits). Usable as a fallback: a tiny "tee" in the Operator's statusline writing `~/.work-agent/claude-rate-limits.json`. This is optional and needs a manual setup step.

## Candidate 4: Reactive detection (terminal output, hooks, Orca agent state)

**Evidence**

- `orca terminal read --terminal <handle> [--cursor n] [--limit n] [--screen] --json` reads bounded, escape-stripped output, and `--screen` reads the rendered frame (`orca terminal read --help`). `orca terminal show` gives metadata and a preview. `orca worktree ps --json` rows carry `preview` and per-agent `lastAssistantMessage`. **The daemon can read the interactive Claude terminal output.**
- Claude Code hooks (code.claude.com/docs/en/hooks): `StopFailure` fires when a turn ends on an API error, with `error_type`, one of `rate_limit`, `overloaded`, `authentication_failed`, `billing_error`, …, and `error_message`. `Notification` has `quota_auto_resume_*` types. These are observational only.
- Orca already hooks `StopFailure` (`~/.claude/settings.json` → `~/.orca/agent-hooks/claude-hook.cmd`). In the bundle (`out/main/index.js`), `StopFailure` maps the agent state to **`done`** and sets `mainAgent: {state:'done', outcome:'failure'}`. `error_type` is **not** propagated (no `error_type` string anywhere in `out/main`). So `worktree ps` shows a limit hit as a failed `done`, which is indistinguishable from other API failures.
- Important for `FixedSlots`: a limit-hit agent is not `working`, so it frees its slot, and the MVP policy would Wake *another* Workspace straight into the same limit. That is a concrete reason for a usage-aware policy.
- Design reference `ableinc/coding-agent-loop` `internal/gate/gate.go` (described, not copied): it does a case-insensitive phrase match on limit wording ("usage limit reached", "rate limit exceeded", "too many requests", …), parses a reset epoch (a `…|<epoch>` form or a ≥9-digit epoch near reset wording), and accepts it only if it falls between now and now+24h. Otherwise it backs off 5 → 15 → 30 → 60 min, adds a 30 s cushion, and treats the hit as *deferred*, not failed (≈ our Held Wake). `DetectAuthExpired` matches OAuth-expiry phrases and treats them as non-self-healing (notify the Operator).
- Option: a daemon-owned `StopFailure` hook with matcher `rate_limit`, writing a marker file. This works, but it adds a second hook install the daemon would have to manage. Not recommended for phase 2 unless terminal parsing proves flaky.

**Stability:** Terminal wording is undocumented and changes between Claude Code versions. Hook `error_type` values are documented. Orca's `done`/`failure` mapping is internal.

**Auth implications:** None.

**Gives:** Reactive detection of a hit, and sometimes a reset time. It is not proactive: the limit is already burned when it fires.

## Candidate 5: Other sources considered

- **Anthropic Admin / Usage & Cost API:** for Console API organisations with admin keys. It does not cover consumer Pro/Max plan windows. Not applicable.
- **Local transcript estimation (ccusage-style, `~/.claude/projects/**/*.jsonl`):** token counts can be summed locally. Anthropic does not publish the plan limits as token budgets, so a % would be a guess. Orca has a "Stats & Usage" scanner (`out/main/usage-scan-worker-entry.js`) for analytics, not for limits. Not suitable as a gate.
- **`~/.claude.json` `cachedUsageUtilization`:** mentioned in anthropics/claude-code#79022 as internal state. **Absent** in this machine's `~/.claude.json` with Claude Code 2.1.288. Undocumented. Rejected.
- **Claude Code `/usage`:** an interactive slash command. Driving it from the daemon would cost a session interaction. Rejected.

## Comparison

| Source | Proactive % + reset | Fable weekly | Documented | Daemon holds credential | Extra API calls by daemon | Works with Orca unfocused | Verdict |
|---|---|---|---|---|---|---|---|
| 1. `orca account list --json` → `rateLimits.claude` | yes | **yes** | no (internal field) | no | none | stale (15-min poll only when focused, live only with managed statusline) | **Primary** |
| 2. Direct `/api/oauth/usage` | yes | yes | no | **yes (ToS conflict)** | yes (429-prone) | yes | Rejected |
| 3. Statusline `rate_limits` tee | yes | no | **yes** | no | none | only while a session is active | Optional fallback |
| 4. Terminal read / `StopFailure` | reactive only | n/a | partial (hooks yes, wording no) | no | none | yes | **Backstop** |
| 5. Admin API / JSONL / `.claude.json` | no or guessed | no | varies | varies | varies | – | Rejected |

## Recommendation for phase 2

**Prerequisite — freshness.** Candidate 1 is only as fresh as Orca's cache. On this machine (Operator-owned `statusLine`, Orca often unfocused) the cache does not refresh while the daemon runs unattended, so without a fix the policy below would sit in its degraded "unknown" mode most of the time. Phase 2 must therefore secure one live feed before relying on the thresholds, in this order of preference:

1. the Orca feature request below lands (`--max-age` refresh / background refresh while agents run), or
2. Orca's managed statusline feed is active — the Operator either drops their own `statusLine` or has it forward the `rate_limits` JSON it already receives to Orca (Candidate 3 data reaching Candidate 1, no credential handling; 5h/7d only, Fable stays poll-only), or
3. the statusline "tee" of Candidate 3 writes a file the daemon reads directly (5h/7d only).

The threshold values below (80% / 90% / 30 min) are suggested starting points for the phase-2 implementer, not decisions made by this spike.

1. **Add a `PlanUsage` signal**, read once per Tick and never stored (same treatment as Orca agent state, ADR-0001). The `workspace.Backend` seam (`internal/workspace/workspace.go`) gets one more read, e.g. `PlanUsage(ctx)`, implemented as `orca.exe account list --json` (argv, absolute path, not `orca.cmd`). The adapter parses only `result.rateLimits.claude.{session,weekly,fableWeekly}.{usedPercent,resetsAt}`, `status`, `error`, and `updatedAt`.
2. **A smart `CapacityPolicy`** wraps `FixedSlots` (or later RAM/CPU). The interface needs the signal passed in, e.g. through the policy's constructor per Tick or an extra argument. Rules:
   - `FreeSlots` is 0 for both kinds when `weekly` ≥ `weeklyHoldPercent` (suggest 80), `fableWeekly` ≥ the same threshold (gated whenever Orca reports it: the daemon does not pin a model, so Wakes run on whatever the Operator's Claude default is, which may be Fable), or `session` ≥ `sessionHoldPercent` (suggest 90). Hold until the earliest relevant `resetsAt`. The resulting Wakes become Held Wakes (why: `capacity`).
   - Degrade, don't freeze. If the snapshot is missing, errored, or `updatedAt` is older than a staleness bound (e.g. 30 min), allow **at most one** working agent in total and notify the Operator ("plan usage unknown"). This avoids both blind bursts and a permanently stalled daemon when Orca is minimised.
   - Optional: reserve headroom for the Operator's own interactive use by lowering thresholds when the Operator is active. Out of scope here.
3. **Backstop.** When `worktree ps` shows a Work Item's agent `done` with `mainAgent.outcome == "failure"`, read its terminal (`orca terminal read --terminal <h> --limit 200 --json`) and run limit and auth-expiry detection modelled on the coding-agent-loop design (phrases, reset epoch, 24h sanity cap, backoff ladder). On a limit hit, set an in-memory "plan limited until T" signal that forces `FreeSlots` to 0, and notify. This state is in memory only, because the next Tick re-derives it from the proactive source. Auth expiry is a notification to the Operator, not a Hold.
4. **Never** read `~/.claude/.credentials.json` or call `/api/oauth/usage` from the daemon.
5. **Correct PRD §9** ("Orca does not expose … Claude plan usage through the CLI") and §19 in the implementation PR, and file the Orca request below.

### Drafted Orca feature request (paste into https://github.com/stablyai/orca/issues)

**Title:** `[Feature]: Documented CLI surface for provider rate limits (orca usage --json)`

**Body:**

> **Problem**
> Local automation that drives coding agents through the Orca CLI, such as a daemon that Wakes Claude Code in Orca worktrees, needs to know how much of the user's Claude plan is left (5-hour session, weekly, per-model weekly such as Fable). Without that, it cannot avoid burning the weekly limit while unattended. Today the only CLI path is an undocumented side effect: `orca account list --json` returns the runtime's `rateLimits` snapshot via `accounts.list`. The human output omits it, `orca agent-context` does not describe it, and no `capabilities[]` entry advertises it. Also, the cache only refreshes on the 15-minute timer while the Orca window is focused (`shouldBackgroundPoll`), or from the managed statusline feed, which is skipped when the user has their own `statusLine`. So an unattended consumer can't tell how fresh the data is or ask for a safe refresh.
>
> **Proposal**
> 1. `orca usage [--provider claude] [--max-age <sec>] --json`. It returns the cached snapshot. With `--max-age`, it refreshes only if the cache is older than that, respecting the existing 429/backoff handling so callers cannot hammer the endpoint.
> 2. Advertise a capability, e.g. `rateLimits.read.v1`, in `orca status --json`, and document the schema in `orca agent-context`.
> 3. Optionally, refresh in the background on a slow cadence while any Claude agent is running, even when the window is unfocused.
>
> **Example output**
> ```json
> { "ok": true, "result": { "claude": {
>     "status": "ok", "error": null, "updatedAt": 1791007266412,
>     "source": "oauth",
>     "session":     { "usedPercent": 30, "windowMinutes": 300,   "resetsAt": 1791016800212 },
>     "weekly":      { "usedPercent": 6,  "windowMinutes": 10080, "resetsAt": 1791460800212 },
>     "modelWeekly": [ { "model": "Fable", "usedPercent": 0, "windowMinutes": 10080, "resetsAt": 1791460800000 } ]
> } } }
> ```
>
> **Why this beats tools reading the token themselves**
> Orca already holds and refreshes the OAuth credential, handles live-session token rotation, and rate-limits its own calls to the usage endpoint. If every integration instead reads `~/.claude/.credentials.json` and calls the endpoint, each one duplicates credential handling, multiplies 429s on an already-throttled endpoint, and runs into Anthropic's terms on intermediating Claude.ai credentials. A documented, read-only Orca surface keeps the credential in one place and gives integrations a stable contract.

## Open questions / not verified

- **Not verified:** the live endpoint response itself (deliberately not called). The response shape above is inferred from Orca's parser and community issues.
- How stale the `rateLimits` snapshot gets in practice with Orca minimised all day. Measure `updatedAt` age over a working day before choosing the staleness bound.
- Whether a remote or headless `orca serve` runtime populates `rateLimits` (no window, so `shouldBackgroundPoll()` is false whenever `mainWindow` is absent). Unverified.
- The exact current wording of Claude Code's interactive usage-limit message, and whether it embeds a reset epoch. Capture a real sample before writing the matcher.
- Whether Orca's statusline live feed would activate if the Operator removed their own `statusLine`. The code suggests yes for Claude Code ≥ 2.1.80. Not tested.
- Whether Anthropic considers Orca's own token use (with a `claude-code/2.1.0` User-Agent) compliant. That is outside the daemon's control, but it is a dependency risk for Candidate 1.
- No existing Orca issue requesting a CLI usage surface was found (searched `stablyai/orca` issues for "usage", "usage cli", "rate limits json").

## Sources

- Orca CLI 1.4.219 output: `orca.exe --help`, `status --json`, `agent-context [--json]`, `account list --json`, `terminal read --help`, `worktree ps --json` (run 2026-10-02).
- Orca bundle `<orca install>/resources/app.asar` and `app.asar.unpacked` (MIT, `package.json` homepage https://github.com/stablyai/orca):
  - `app.asar.unpacked/out/cli/handlers/account.js` (`accounts.list`, `refreshUsage: false`); `out/shared/rpc-contract/accounts-params.js`
  - `app.asar` → `out/main/index.js` (RPC table with `accounts.list`/`accounts.subscribe`; usage fetch `https://api.anthropic.com/api/oauth/usage` with headers; Fable `weekly_scoped` parsing; credentials-file/keychain lookup; poll interval `900*1e3`, min `30*1e3`, `shouldBackgroundPoll`; `rateLimits:*` IPC; `/statusline/claude` ingest; `StopFailure` → `done`/`failure`)
  - `app.asar` → `out/shared/claude-statusline-rate-limits.js` (statusline feed, 429 note)
  - `app.asar` → `out/main/chunks/managed-agent-hook-controls-Ca9KzOKu.js` (managed statusline install only when user has none)
- https://github.com/stablyai/orca and https://github.com/stablyai/orca/issues
- https://code.claude.com/docs/en/statusline (`rate_limits` fields and presence rules)
- https://code.claude.com/docs/en/hooks (`StopFailure` `error_type` values, `Notification` quota types)
- https://code.claude.com/docs/en/legal-and-compliance ("Authentication and credential use")
- https://github.com/anthropics/claude-code/issues/31021, /34346, /79022 (endpoint 429s, fields, per-model windows not in statusline)
- https://github.com/ableinc/coding-agent-loop `internal/gate/gate.go` (design reference only; unlicensed, not copied) and `docs/reference/coding-agent-loop.md`
- Repo: `CONTEXT.md`, `docs/adr/0001-work-item-state-from-github-only.md`, `docs/work-agent-prd-updated.md` §2, §9, §11, §19, §20, `internal/workflow/capacity.go`
