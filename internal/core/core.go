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
	"github.com/dwatts1772/work-agent-daemon/internal/notify"
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
	// notifiers always starts with the console/JSONL log.
	notifiers *notify.Multi
	once      *notify.Once
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
	notifiers := &notify.Multi{notify.NewLog(log)}
	return &Daemon{
		cfg:        cfg,
		github:     gh,
		workspaces: workspace.NewOrca(runner),
		log:        log,
		notifiers:  notifiers,
		once:       notify.NewOnce(notifiers),
	}, nil
}

// OrcaAvailable reports whether an Orca runtime is reachable. It is a
// signal read each Tick, never stored (ADR-0001).
func (d *Daemon) OrcaAvailable(ctx context.Context) bool {
	ok, err := d.workspaces.Available(ctx)
	if err != nil {
		d.log.Warn("orca unavailable", "err", err)
	}
	return ok
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
// carries out the Workspace actions that follow, saves each outcome as it
// happens, and notifies the Operator of what now needs their attention.
// The caller holds store's lock for the whole Tick.
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
	if err != nil {
		return res, err
	}
	final, err := store.Load()
	if err != nil {
		return res, err
	}
	d.notify(ctx, final, len(pending) > 0, res.Held)
	return res, nil
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
// PRs are observed before missing issues, so a merged PR reaches Done
// with the PR linked rather than via its closed issue. observe sees every
// allowlisted repo, so a tracked Owned Issue it does not report is closed or
// no longer Eligible.
func (d *Daemon) reconcile(ctx context.Context, current workflow.State) (workflow.State, int, []workflow.Action, error) {
	now := time.Now().UTC()
	assigned, err := d.observe(ctx, now)
	if err != nil {
		return workflow.State{}, 0, nil, err
	}
	next, actions := workflow.Reconcile(current, assigned)

	prEvents, err := d.observePRs(ctx, next, now)
	if err != nil {
		return workflow.State{}, 0, nil, err
	}
	next, prActions := workflow.Reconcile(next, prEvents)
	actions = append(actions, prActions...)

	// An issue that cannot be read, such as a deleted one, is treated as no
	// longer Eligible: it is Paused, never lost, and never fails the Tick.
	missing := workflow.Ineligible(next, assigned, now)
	for i, e := range missing {
		closed, err := d.github.IssueClosed(ctx, e.Repo, e.Issue)
		if err != nil {
			d.log.Warn("cannot tell whether a missing Owned Issue is closed; treating it as not Eligible", "item", e.Ref(), "err", err)
			continue
		}
		if closed {
			missing[i].Type = workflow.IssueClosed
		}
	}
	next, missingActions := workflow.Reconcile(next, missing)
	actions = append(actions, missingActions...)

	d.log.Info("tick", "eligible", len(assigned), "actions", len(actions))
	return next, len(assigned), actions, nil
}

// observePRs lists the Operator's PRs in every repo with an Owned Issue not
// yet Done, and turns each such item's PRs into at most one event.
func (d *Daemon) observePRs(ctx context.Context, st workflow.State, now time.Time) ([]workflow.Event, error) {
	prs := map[string][]github.PullRequest{}
	var events []workflow.Event
	for _, w := range st.Items {
		if !w.Active() {
			continue
		}
		repoPRs, ok := prs[w.Repo]
		if !ok {
			var err error
			if repoPRs, err = d.github.OperatorPullRequests(ctx, w.Repo); err != nil {
				return nil, fmt.Errorf("list pull requests in %s: %w", w.Repo, err)
			}
			prs[w.Repo] = repoPRs
		}
		if e, ok := prEvent(w, repoPRs, now); ok {
			events = append(events, e)
		}
	}
	return events, nil
}

// prEvent decides what w's PRs, newest first, say about it: the linked PR
// being merged ends the item; otherwise an open PR is discovered, the newest
// one winning; with none open, the linked PR (or else the newest) being
// merged or closed ends the item.
func prEvent(w workflow.WorkItem, prs []github.PullRequest, now time.Time) (workflow.Event, bool) {
	branch := ""
	if w.Workspace != nil {
		branch = w.Workspace.Branch
	}
	var open, ended *github.PullRequest
	for i := range prs {
		pr := &prs[i]
		if !pr.Covers(w.Repo, w.Issue, branch) {
			continue
		}
		switch {
		case pr.State == github.PROpen:
			if open == nil {
				open = pr
			}
		case ended == nil || pr.Number == w.PR:
			ended = pr
		}
	}
	e := workflow.Event{Repo: w.Repo, Issue: w.Issue, Title: w.Title, URL: w.IssueURL, ObservedAt: now}
	switch {
	case open != nil && !(ended != nil && ended.Number == w.PR && ended.State == github.PRMerged):
		e.Type, e.PR, e.PRURL = workflow.PRDiscovered, open.Number, open.URL
	case ended != nil && ended.State == github.PRMerged:
		e.Type, e.PR, e.PRURL = workflow.PRMerged, ended.Number, ended.URL
	case ended != nil:
		e.Type, e.PR, e.PRURL = workflow.PRClosed, ended.Number, ended.URL
	default:
		return workflow.Event{}, false
	}
	return e, true
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
func (d *Daemon) observe(ctx context.Context, now time.Time) ([]workflow.Event, error) {
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
