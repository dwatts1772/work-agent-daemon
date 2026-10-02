// Package core wires the daemon together. It is embedded by both the
// headless CLI and the tray app, and must never import Wails (ADR-0004).
package core

import (
	"context"
	"fmt"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/github"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/process"
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

// Tick observes GitHub and returns the Eligible issues across the allowlisted
// repos.
func (d *Daemon) Tick(ctx context.Context) ([]github.Issue, error) {
	var eligible []github.Issue
	for _, repo := range d.cfg.GitHub.Repos {
		issues, err := d.github.EligibleIssues(ctx, repo, d.cfg.GitHub.EligibilityLabel)
		if err != nil {
			return nil, fmt.Errorf("list Eligible issues in %s: %w", repo, err)
		}
		eligible = append(eligible, issues...)
	}
	d.log.Info("tick", "eligible", len(eligible))
	return eligible, nil
}
