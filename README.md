# Work Agent Daemon

A local, LLM-free daemon that watches GitHub on behalf of one developer — the **Operator** — and hands Eligible work to Claude Code in isolated Orca **Workspaces**. GitHub is the source of truth; the daemon orchestrates, Claude does the engineering judgment. It runs on Windows and macOS as a system-tray app (`work-agent-tray`) and a headless CLI (`work-agent`) over the same core.

On every **Tick** (every 45 s by default) the daemon:

- turns each **Eligible** issue — assigned to the Operator, carrying the Eligibility Label, in an allowlisted repo — into an **Owned Issue**, creates its Workspace through Orca and **Wakes** Claude there with `/work-item issue <owner/repo>#<n>`;
- links the draft PR Claude opens, and Wakes the same Workspace (resuming the same Claude conversation) for review feedback once a review is submitted or a **Quiet Period** passes, and for a **Settled** CI failure, once per head commit;
- marks the PR `READY_TO_MERGE` and notifies you when CI is green and no actionable feedback remains — **it never merges**; you do, and the Work Item becomes `DONE`;
- turns each pull request that requests your review into a **Review Request**: once its CI is Settled it builds a Review Workspace from the pull ref and Wakes `/work-item review <owner/repo>#<n>`; the findings are **held for your approval**, never submitted for you.

Vocabulary is defined in [`CONTEXT.md`](CONTEXT.md), the product in [`docs/work-agent-prd-updated.md`](docs/work-agent-prd-updated.md), and decisions in [`docs/adr/`](docs/adr/).

## Prerequisites

| Tool | Why | Windows | macOS |
| --- | --- | --- | --- |
| Go (version in `go.mod`) | build both binaries | `winget install GoLang.Go` | `brew install go` |
| Node.js 22 + npm | build the status window | `winget install OpenJS.NodeJS.LTS` | `brew install node@22` |
| Git | Workspaces, pull refs | `winget install Git.Git` | Xcode Command Line Tools or `brew install git` |
| GitHub CLI `gh` | every GitHub read | `winget install GitHub.cli` | `brew install gh` |
| Orca (1.4.219 or later) | owns Workspaces and terminals | Orca installer | Orca installer |
| [Claude Code](https://docs.claude.com/en/docs/claude-code) `claude` | does the work | `irm https://claude.ai/install.ps1 \| iex` | `curl -fsSL https://claude.ai/install.sh \| bash` |
| [mattpocock-skills](https://github.com/mattpocock/skills) plugin | the default Routing targets it | in Claude Code: `/plugin install mattpocock-skills@claude-plugins-official` | same |

### Orca

The daemon talks to a **running** Orca — the desktop app, or headless `orca serve` — and never launches it. While Orca is closed, Ticks keep observing GitHub and every Workspace action becomes a Held Wake, retried on the first Tick after Orca is back. Check it with:

```sh
orca status --json        # "runtime": { "reachable": true, ... }
```

Every repo you allowlist must be registered in Orca from a local clone whose `origin` (or other remote) points at GitHub:

```sh
git clone git@github.com:<owner>/<repo>.git
orca repo add --path <path-to-clone>
```

On Windows the daemon runs `orca.exe`, never the `orca.cmd` shim beside it (the shim drops message bodies).

## Build

Clone this repo, then build from its root (PowerShell on Windows, any shell on macOS):

```sh
git clone https://github.com/dwatts1772/work-agent-daemon.git
cd work-agent-daemon

go build -o bin/ ./cmd/work-agent

npm --prefix cmd/work-agent-tray/frontend ci
npm --prefix cmd/work-agent-tray/frontend run build
```

Then the tray app — a GUI-subsystem binary on Windows, so it opens no console window:

```powershell
# Windows
go build -ldflags -H=windowsgui -o bin/ ./cmd/work-agent-tray
```

```sh
# macOS
go build -o bin/ ./cmd/work-agent-tray
```

Put `bin/` somewhere stable before turning on start at login: the login item records the binary's absolute path.

`go test ./...` runs every test against stub `gh` / `git` / `orca` / `claude` binaries; nothing touches GitHub or Orca.

## Pin the Operator's GitHub account

The daemon acts as exactly one GitHub account, `github.account` in the config. Every `gh` child process gets `GH_TOKEN` from `gh auth token --user <account>`, and at startup the daemon checks that `gh api user` returns that login — **it refuses to run otherwise**. It never uses `gh`'s active account or `@me`, so it is safe on a machine with several accounts logged in; do not `gh auth switch` for it.

```sh
gh auth login                                  # once per account, choosing github.com
gh auth status                                 # the Operator account must be listed
gh auth token --user <account>                 # must print a token
```

Git pushes from Workspaces use your normal git credentials (e.g. an SSH key for the Operator account); the daemon itself never pushes.

## Configure

Create `~/.work-agent/config.json` (`%USERPROFILE%\.work-agent\config.json` on Windows). That directory is the **state directory**: `state.json` and `logs/` are written beside the config, and `--config <path>` moves all three.

```json
{
  "github": {
    "account": "your-github-login",
    "repos": ["your-org/your-repo"],
    "eligibilityLabel": "agent-ready",
    "pollIntervalSeconds": 45,
    "quietPeriodMinutes": 5,
    "feedbackBots": ["coderabbitai[bot]"]
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
  }
}
```

| Key | Required | Meaning |
| --- | --- | --- |
| `github.account` | yes | the Operator's GitHub login |
| `github.repos` | yes | allowlist of `owner/name` repos; nothing else is ever touched |
| `github.eligibilityLabel` | yes | an assigned issue becomes an Owned Issue only with this label; removing it Pauses the Work Item, re-adding resumes it |
| `github.pollIntervalSeconds` | no (45) | tray app Tick interval |
| `github.quietPeriodMinutes` | no (5) | Quiet Period for standalone comments and for a Review Request's new head |
| `github.feedbackBots` | no | bot logins whose feedback Wakes Claude; otherwise only authors with write access (OWNER / MEMBER / COLLABORATOR) count |
| `claude.entrySkill` | no (`/work-item`) | the skill every Wake invokes; `/work-agent:work-item` when installed as a plugin |
| `capacity.*Slots` | no (1 each) | how many agents of each kind may be `working` at once; Held Wakes wait for a free slot |
| `notify.desktop` | no (true) | the tray app's native notifications; the console/JSONL log is always on |
| `binaries` | no | absolute paths for `gh`, `git`, `orca`, `claude` — see [Troubleshooting](#troubleshooting) |

Create the Eligibility Label in each allowlisted repo:

```sh
gh label create agent-ready --repo your-org/your-repo --description "Approved for agent work"
```

## Install the Entry Skill

Every Wake types `<entrySkill> <reason> <owner/repo>#<n>` into the Workspace's Claude session. The Entry Skill `/work-item` lives in [`skills/work-item/`](skills/work-item/); see [`skills/README.md`](skills/README.md). Install it one of two ways:

- **Claude Code plugin** — in Claude Code: `/plugin marketplace add dwatts1772/work-agent-daemon`, then `/plugin install work-agent@work-agent`. Plugin skills are namespaced, so set `"claude": { "entrySkill": "/work-agent:work-item" }`.
- **Personal skill**, linked from your clone so it updates with it (keeps the default `/work-item`):

  ```powershell
  # Windows (PowerShell)
  New-Item -ItemType Junction -Path "$HOME\.claude\skills\work-item" -Target "$PWD\skills\work-item"
  ```

  ```sh
  # macOS
  mkdir -p ~/.claude/skills && ln -s "$PWD/skills/work-item" ~/.claude/skills/work-item
  ```

Routing — which workflow each Wake Reason follows — is layered markdown read by Claude, never by the daemon: the built-in default `skills/work-item/routing.md`, then your override `~/.work-agent/routing.md`, then a repo's `docs/agents/work-item-routing.md`. The push policy holds under every layer: Owned Issue Workspaces push (never force) only their own branch and open **draft** PRs; Review Workspaces never push.

## Run

First prove the config and the Operator account with a dry run — it observes GitHub and prints what a Tick would do, touching neither Orca, Claude nor `state.json`:

```sh
bin/work-agent tick --dry-run
```

Then either run the tray app, which Ticks in the background:

```sh
bin/work-agent-tray            # bin\work-agent-tray.exe on Windows
```

or drive it Tick by Tick from the CLI:

```sh
work-agent tick [--dry-run]                 # one Tick, then exit
work-agent list                             # every Work Item with its state
work-agent inspect <owner/name>#<n>         # one Work Item in full
work-agent pause   <owner/name>#<n>         # no more Wakes; nothing is deleted
work-agent resume  <owner/name>#<n>
work-agent wake    <owner/name>#<n>         # carry out a Held Wake now
```

The CLI and the tray app share a lock on the state directory, so they never Tick at once; a `tick` while the tray app is mid-Tick fails fast.

The tray menu has **Open status window** (every Work Item with its state, Workspace, PR and Held Wake reason, with pause / resume / wake buttons), **Pause all**, **Tick now**, **Start at login** and **Quit**. The icon shows the overall status: OK, Held Wakes, Orca unavailable, or needs the Operator. Desktop notifications fire when Orca is unavailable, a Work Item is Paused or `FAILED`, a PR is `READY_TO_MERGE`, review findings are ready for your approval, or an agent is waiting on you.

### A Work Item's life

```text
Owned Issue:    PENDING_WORKSPACE → IN_PROGRESS → WAITING_FOR_CI ⇄ ADDRESSING_FEEDBACK
                                                  → WAITING_FOR_REVIEW → READY_TO_MERGE → (you merge) → DONE
Review Request: PENDING_WORKSPACE → REVIEWING → REVIEWED (findings held for you) → DONE when merged/closed
```

`READY_TO_MERGE` needs CI Settled green, no actionable feedback and no standing change request; it needs no approval, because Claude's PR is a draft that only you mark ready. It is the hand-off to you: mark it ready, get whatever review your branch protection asks for, merge. GitHub still enforces branch protection when you merge.

Feedback counts only from write-access reviewers other than the Operator, or a `feedbackBots` entry: Claude comments as you, so counting your own comments would Wake Claude on its own replies. On a repo where you are the only collaborator, a feedback Wake comes only from a configured bot (or you steer Claude in its Workspace yourself), and no Review Request ever exists, since one needs another developer's PR ([#37](https://github.com/dwatts1772/work-agent-daemon/issues/37)).

The daemon never merges, force-pushes, deploys, marks a PR ready for review or submits a review: those stay with you. It never deletes branches or Workspaces either; `DONE` Review Workspaces are listed in the status window as safe to clean up.

## Start at login

Tick **Start at login** in the tray menu. It registers the tray app's absolute path and your config's absolute path as a Windows startup entry (`HKCU\...\Run`) or a macOS login item (a LaunchAgent). Untick it to remove the entry. There is no supervisor: if the tray app crashes it is not restarted, and a manual restart is safe because every Tick reconciles against GitHub.

## Troubleshooting

**`refusing to run` / `refusing to start` at startup.** Read the reason after it:

- *config* errors name the bad key; fix `config.json`.
- the Operator check failed — `gh auth token --user <account>` must succeed and `gh api user` with that token must return `<account>`. Run `gh auth login` for the account.
- `the tray app is already running` — another tray app is using this state directory. Only one tray app runs per state directory.

**`gh was not found on PATH; set binaries.gh in config` — usually only from start at login.** A login item or startup entry gets a minimal `PATH`, unlike your terminal. The daemon resolves `gh`, `git`, `orca` and `claude` to absolute paths at startup — from `PATH`, then the usual install locations (Windows: `Program Files\GitHub CLI`, `Program Files\Git\cmd`, `%LOCALAPPDATA%\Programs\orca\resources\bin`, `~\.local\bin`, `%APPDATA%\npm`; macOS: `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin`, `~/.local/bin`). If yours live elsewhere, pin them:

```json
"binaries": {
  "gh": "C:\\Program Files\\GitHub CLI\\gh.exe",
  "orca": "C:\\Users\\you\\AppData\\Local\\Programs\\orca\\resources\\bin\\orca.exe",
  "claude": "/Users/you/.local/bin/claude"
}
```

Find the paths with `Get-Command gh, git, orca.exe, claude` (PowerShell) or `which gh git orca claude` (macOS). On Windows `binaries.orca` must be `orca.exe`.

**Start at login does nothing.**

- Windows: check *Settings → Apps → Startup* lists the app and is on. Moving or rebuilding the binary to a new path breaks the entry — untick and re-tick **Start at login**.
- macOS: check *System Settings → General → Login Items & Extensions*; macOS may have asked you to allow the new background item. The entry is a LaunchAgent in `~/Library/LaunchAgents/`. Again, re-tick after moving the binary.
- Either way, the log at `~/.work-agent/logs/` says why a login-started tray app exited.

**Workspace actions stay Held.** The status window and `work-agent inspect <owner/name>#<n>` give the reason: Orca unavailable (start Orca), the agent is `working` (it Wakes when the agent is idle), no capacity slot (raise `capacity.*Slots` or wait), or a Quiet Period that has not elapsed. A repo not registered in Orca is logged as `<repo> is not registered in Orca` — run `orca repo add --path <clone>`.

**An issue is not picked up.** It must be open, assigned to `github.account`, labelled with `github.eligibilityLabel`, and in a repo in `github.repos`. `work-agent tick --dry-run` shows what the daemon sees.

**A Wake types the command but Claude says the skill is unknown.** The Entry Skill is not installed for that Claude, or `claude.entrySkill` does not match how it was installed (`/work-item` linked, `/work-agent:work-item` as a plugin).

**Logs.** Every Tick and external action is logged as JSONL to `~/.work-agent/logs/work-agent.jsonl`, with tokens redacted.
