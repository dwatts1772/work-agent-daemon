# Skills

`work-item/` is the Entry Skill every Wake invokes (`/work-item <reason> <owner/repo>#<n>`), with its built-in default Routing in `work-item/routing.md`. It routes to [mattpocock-skills](https://github.com/mattpocock/skills) by default, so install that plugin too.

Install it one of two ways:

- **Claude Code plugin** — `/plugin marketplace add dwatts1772/work-agent-daemon`, then `/plugin install work-agent@work-agent`. Plugin skills are namespaced, so set `claude.entrySkill` to `/work-agent:work-item`.
- **Personal skill** — link the folder into `~/.claude/skills`, keeping the default `claude.entrySkill` of `/work-item`:
  - macOS: `ln -s "$PWD/skills/work-item" ~/.claude/skills/work-item`
  - Windows (PowerShell): `New-Item -ItemType Junction -Path "$HOME\.claude\skills\work-item" -Target "$PWD\skills\work-item"`

Override Routing with `~/.work-agent/routing.md` (Operator) or `docs/agents/work-item-routing.md` in a target repo (repo); both use the table format of `work-item/routing.md`.

`go test ./skills/` pins the default Routing, the layer merge, and the push policy. `WORK_AGENT_LIVE_EVAL=1 go test ./skills/ -run Live` additionally asks a real `claude -p` to resolve Routing through fixture overrides.
