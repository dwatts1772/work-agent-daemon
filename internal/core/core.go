// Package core wires the daemon together. It is embedded by both the
// headless CLI and the tray app, and must never import Wails (ADR-0004).
package core

import (
	"context"
	"fmt"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/github"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/process"
	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// Daemon is a started daemon whose GitHub access is verified to act as the
// Operator.
type Daemon struct {
	cfg    config.Config
	github *github.Client
	log    *logging.Logger
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
	return &Daemon{cfg: cfg, github: gh, log: log}, nil
}

// Result is what one Tick observed and decided.
type Result struct {
	// Eligible counts the Eligible issues observed.
	Eligible int
	Actions  []workflow.Action
}

// Tick observes GitHub, reconciles the Work Items in store against it, and
// saves the result. The caller holds store's lock for the whole Tick.
func (d *Daemon) Tick(ctx context.Context, store *state.Store) (Result, error) {
	current, err := store.Load()
	if err != nil {
		return Result{}, err
	}
	next, res, err := d.plan(ctx, current)
	if err != nil {
		return Result{}, err
	}
	if len(res.Actions) > 0 {
		if err := store.Save(next); err != nil {
			return Result{}, fmt.Errorf("save state: %w", err)
		}
	}
	for _, a := range res.Actions {
		d.log.Info("action", "action", a.Type, "item", a.Item.ID)
	}
	return res, nil
}

// DryRun observes GitHub and returns the actions a Tick would take from
// current, changing nothing.
func (d *Daemon) DryRun(ctx context.Context, current workflow.State) (Result, error) {
	_, res, err := d.plan(ctx, current)
	return res, err
}

func (d *Daemon) plan(ctx context.Context, current workflow.State) (workflow.State, Result, error) {
	events, err := d.observe(ctx)
	if err != nil {
		return workflow.State{}, Result{}, err
	}
	next, actions := workflow.Reconcile(current, events)
	d.log.Info("tick", "eligible", len(events), "actions", len(actions))
	return next, Result{Eligible: len(events), Actions: actions}, nil
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
