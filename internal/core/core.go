// Package core wires the daemon together. It is embedded by both the
// headless CLI and the tray app, and must never import Wails (ADR-0004).
package core

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/github"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/process"
	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

// Daemon is a started daemon whose GitHub access is verified to act as the
// Operator.
type Daemon struct {
	cfg        config.Config
	github     *github.Client
	workspaces workspace.Backend
	log        *logging.Logger
}

// Start resolves the binaries and verifies the Operator. It fails if gh
// cannot act as the configured account; the daemon must not run then.
func Start(ctx context.Context, cfg config.Config, log *logging.Logger, searchDirs []string) (*Daemon, error) {
	bins, err := process.ResolveBinaries(cfg.Binaries, searchDirs)
	if err != nil {
		return nil, err
	}
	runner := process.NewRunner(bins, log)
	gh, err := github.Connect(ctx, runner, log, cfg.GitHub.Account)
	if err != nil {
		return nil, err
	}
	log.Info("operator verified", "account", cfg.GitHub.Account)
	return &Daemon{cfg: cfg, github: gh, workspaces: workspace.NewOrca(runner), log: log}, nil
}

// Result is what one Tick observed and decided.
type Result struct {
	// Eligible counts the Eligible issues observed.
	Eligible int
	Actions  []workflow.Action
	// Held is set when the Tick Held its Workspace actions because Orca is
	// unavailable; they are retried on the next Tick.
	Held bool
}

// Tick observes GitHub, reconciles the Work Items in store against it,
// carries out the Workspace actions that follow, and saves each outcome as
// it happens. The caller holds store's lock for the whole Tick.
func (d *Daemon) Tick(ctx context.Context, store *state.Store) (Result, error) {
	current, err := store.Load()
	if err != nil {
		return Result{}, err
	}
	next, eligible, observed, err := d.reconcile(ctx, current)
	if err != nil {
		return Result{}, err
	}
	if len(observed) > 0 {
		if err := store.Save(next); err != nil {
			return Result{}, fmt.Errorf("save state: %w", err)
		}
	}
	pending := workflow.PendingActions(next)
	res := Result{Eligible: eligible, Actions: append(observed, pending...)}
	for _, a := range res.Actions {
		d.log.Info("action", "action", a.Type, "item", a.Item.ID)
	}
	res.Held, err = d.act(ctx, store, next, pending)
	return res, err
}

// DryRun observes GitHub and returns the actions a Tick would take from
// current, changing nothing and touching neither Orca nor Claude.
func (d *Daemon) DryRun(ctx context.Context, current workflow.State) (Result, error) {
	next, eligible, observed, err := d.reconcile(ctx, current)
	if err != nil {
		return Result{}, err
	}
	return Result{Eligible: eligible, Actions: append(observed, workflow.PendingActions(next)...)}, nil
}

// reconcile observes GitHub and applies what it saw to current, returning
// the new state, how many Eligible issues were seen, and the actions taken.
// observe sees every allowlisted repo, so a tracked Owned Issue it does not
// report is no longer Eligible.
func (d *Daemon) reconcile(ctx context.Context, current workflow.State) (workflow.State, int, []workflow.Action, error) {
	assigned, err := d.observe(ctx)
	if err != nil {
		return workflow.State{}, 0, nil, err
	}
	events := append(assigned, workflow.Ineligible(current, assigned, time.Now().UTC())...)
	next, actions := workflow.Reconcile(current, events)
	d.log.Info("tick", "eligible", len(assigned), "actions", len(actions))
	return next, len(assigned), actions, nil
}

// act carries out the pending Workspace actions against Orca, saving after
// every outcome so a crash never forgets a created Workspace. When Orca is
// unavailable every Wake is Held instead, to be retried next Tick; Orca is
// never launched. It reports whether it Held.
func (d *Daemon) act(ctx context.Context, store *state.Store, st workflow.State, actions []workflow.Action) (bool, error) {
	if len(actions) == 0 {
		return false, nil
	}
	commit := func(next workflow.State) error {
		if reflect.DeepEqual(next, st) {
			return nil
		}
		st = next
		if err := store.Save(st); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
		return nil
	}

	if ok, err := d.workspaces.Available(ctx); !ok {
		d.log.Warn("Orca is unavailable; holding Workspace actions until a later Tick", "err", err)
		next := st
		for _, a := range actions {
			if a.Type == workflow.Wake {
				next = workflow.Held(next, a.Item.ID, a.Reason, workflow.HoldBackendUnavailable, time.Now().UTC())
			}
		}
		return true, commit(next)
	}

	created := map[string]bool{}
	for _, a := range actions {
		item, ok := st.Item(a.Item.ID)
		if !ok || item.State != workflow.PendingWorkspace {
			continue
		}
		var next workflow.State
		switch a.Type {
		case workflow.CreateWorkspace:
			if item.Workspace != nil {
				continue
			}
			ws, err := d.workspaces.CreateForIssue(ctx, workspace.CreateInput{Repo: item.Repo, Issue: item.Issue})
			if err != nil {
				next = d.failed(st, item.ID, "create Workspace", err)
				break
			}
			created[item.ID] = true
			d.log.Info("Workspace created", "item", item.ID, "path", ws.Path, "orcaIdentityKey", ws.OrcaIdentityKey)
			next = workflow.WorkspaceCreated(st, item.ID, ws, time.Now().UTC())
		case workflow.Wake:
			if item.Workspace == nil {
				continue
			}
			next = d.wake(ctx, st, item, a.Reason, created[item.ID])
		default:
			continue
		}
		if err := commit(next); err != nil {
			return false, err
		}
	}
	return false, nil
}

// wake Wakes Claude in item's Workspace and returns the recorded outcome. A
// Workspace recorded on an earlier Tick is checked first, so a Workspace
// removed in Orca is reported rather than silently recreated.
func (d *Daemon) wake(ctx context.Context, st workflow.State, item workflow.WorkItem, reason workflow.WakeReason, justCreated bool) workflow.State {
	ws := *item.Workspace
	if !justCreated {
		exists, err := d.workspaces.Exists(ctx, ws)
		if err != nil {
			return d.failed(st, item.ID, "check Workspace", err)
		}
		if !exists {
			return d.failed(st, item.ID, "check Workspace", fmt.Errorf("Workspace %s no longer exists in Orca", ws.Path))
		}
	}
	prompt := workflow.WakePrompt(d.cfg.Claude.EntrySkill, reason, item.ID)
	if err := d.workspaces.Wake(ctx, ws, prompt); err != nil {
		return d.failed(st, item.ID, "Wake", err)
	}
	d.log.Info("Woken", "item", item.ID, "reason", reason, "claudeSessionId", ws.ClaudeSessionID)
	return workflow.Woken(st, item.ID, time.Now().UTC())
}

// failed records a failure of the daemon's own action.
func (d *Daemon) failed(st workflow.State, id, action string, err error) workflow.State {
	next := workflow.ActionFailed(st, id, action+": "+err.Error(), time.Now().UTC())
	if item, _ := next.Item(id); item.State == workflow.Failed {
		d.log.Error("Work Item FAILED", "item", id, "action", action, "failures", item.ConsecutiveActionFailures, "err", err)
	} else {
		d.log.Warn("action failed; retrying next Tick", "item", id, "action", action, "failures", item.ConsecutiveActionFailures, "err", err)
	}
	return next
}

// observe turns the Eligible issues across the allowlisted repos into events.
func (d *Daemon) observe(ctx context.Context) ([]workflow.Event, error) {
	now := time.Now().UTC()
	var events []workflow.Event
	for _, repo := range d.cfg.GitHub.Repos {
		issues, err := d.github.EligibleIssues(ctx, repo, d.cfg.GitHub.EligibilityLabel)
		if err != nil {
			return nil, fmt.Errorf("list Eligible issues in %s: %w", repo, err)
		}
		for _, i := range issues {
			events = append(events, workflow.Event{
				Type:       workflow.IssueAssigned,
				Repo:       i.Repo,
				Issue:      i.Number,
				Title:      i.Title,
				URL:        i.URL,
				ObservedAt: now,
			})
		}
	}
	return events, nil
}
