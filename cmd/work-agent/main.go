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
	// A Tick currently only observes GitHub, so --dry-run changes nothing yet;
	// it is accepted so scripts can rely on it.
	fs.Bool("dry-run", false, "observe GitHub and print what would happen, changing nothing")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	logFile, err := openLog(filepath.Dir(*configPath))
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	defer logFile.Close()
	log := logging.New(stderr, logFile)

	ctx := context.Background()
	daemon, err := core.Start(ctx, cfg, log, process.DefaultSearchDirs())
	if err != nil {
		fmt.Fprintln(stderr, "work-agent: refusing to run:", log.Redact(err.Error()))
		return 1
	}
	issues, err := daemon.Tick(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", log.Redact(err.Error()))
		return 1
	}

	if len(issues) == 0 {
		fmt.Fprintln(stdout, "No Eligible issues.")
		return 0
	}
	for _, i := range issues {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", i.Ref(), i.Title, i.URL)
	}
	return 0
}

// openLog opens the JSONL log in the logs/ directory beside the config.
func openLog(stateDir string) (*os.File, error) {
	dir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "work-agent.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}
