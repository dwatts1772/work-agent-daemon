## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues (`dwatts1772/work-agent-daemon`) via the `gh` CLI. Always run `gh` as `dwatts1772` by setting `GH_TOKEN=$(gh auth token --user dwatts1772)`; never `gh auth switch`. See `docs/agents/issue-tracker.md`.

### Triage labels

Uses the five default triage labels (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
