// Command work-agent is the headless CLI for the work agent daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/core"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/process"
	"github.com/dwatts1772/work-agent-daemon/internal/state"
)

const usage = `usage: work-agent tick [--dry-run] [--config path]`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "tick" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	defaultConfig, err := config.DefaultPath()
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	fs := flag.NewFlagSet("tick", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfig, "path to config.json")
	dryRun := fs.Bool("dry-run", false, "observe GitHub and print the actions a Tick would take, writing nothing")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	// The state directory holds config.json, state.json and logs/.
	stateDir := filepath.Dir(*configPath)

	if *dryRun {
		// A dry run writes nothing anywhere — not even the log file.
		return tick(stdout, stderr, logging.New(stderr, io.Discard), cfg, func(ctx context.Context, d *core.Daemon) (core.Result, error) {
			current, err := state.Read(stateDir)
			if err != nil {
				return core.Result{}, err
			}
			return d.DryRun(ctx, current)
		})
	}

	// Take the lock before anything else, so a second Tick fails fast.
	store, err := state.Open(stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	defer store.Close()
	logFile, err := openLog(stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	defer logFile.Close()
	return tick(stdout, stderr, logging.New(stderr, logFile), cfg, func(ctx context.Context, d *core.Daemon) (core.Result, error) {
		return d.Tick(ctx, store)
	})
}

func tick(stdout, stderr io.Writer, log *logging.Logger, cfg config.Config, act func(context.Context, *core.Daemon) (core.Result, error)) int {
	ctx := context.Background()
	daemon, err := core.Start(ctx, cfg, log, process.DefaultSearchDirs())
	if err != nil {
		fmt.Fprintln(stderr, "work-agent: refusing to run:", log.Redact(err.Error()))
		return 1
	}
	res, err := act(ctx, daemon)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", log.Redact(err.Error()))
		return 1
	}
	printResult(stdout, res)
	return 0
}

func printResult(w io.Writer, res core.Result) {
	if res.Eligible == 0 {
		fmt.Fprintln(w, "No Eligible issues.")
		return
	}
	if len(res.Actions) == 0 {
		fmt.Fprintf(w, "No changes: %d Eligible issues, all already Owned Issues.\n", res.Eligible)
		return
	}
	for _, a := range res.Actions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.Type, a.Item.ID, a.Item.State, a.Item.Title, a.Item.IssueURL)
	}
}

// openLog opens the JSONL log in the logs/ directory of the state directory.
func openLog(stateDir string) (*os.File, error) {
	dir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "work-agent.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}
