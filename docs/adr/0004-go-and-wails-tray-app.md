# Go with a Wails v3 tray app, plus a headless CLI on the same core

The daemon is written in Go and ships as a Wails v3 system-tray app (status, Work Item list, pause/resume, native notifications) from the MVP onward, replacing the PRD's original TypeScript/Node plan. Go gives a single static binary per platform with no runtime install, good child-process control, and the same language as the `coding-agent-loop` reference design. Wails v3 is chosen over v2 because only v3 has a system tray, notifications, and multi-window support — accepting that v3 is in beta, so the version is pinned exactly and upgraded deliberately.

## Consequences

- The core (reducer, GitHub, Orca adapter, state, capacity, notifier interface) is a plain Go package with no Wails import; it is embedded by two entrypoints: the Wails tray app and a headless `work-agent` CLI (`tick`, `--dry-run`, tests). A Windows GUI-subsystem binary cannot reliably write to a console, hence two binaries.
- Supervision is "start at login" for the tray app instead of launchd / Task Scheduler. The OS no longer restarts it on crash; reconciliation makes a manual restart safe.
- Orca remains the place the Operator interacts with agents; the tray app shows orchestration status, it does not duplicate Workspace/terminal UI.

## Considered Options

- Go core now, Wails in phase 2 — avoids building on a beta, but the Operator wanted the framework from the start.
- TypeScript/Node with a later Electron/Tauri UI — two runtimes to ship, weaker process control on Windows.
