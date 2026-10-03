package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/core"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/process"
	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// itemRef matches a Work Item reference, "owner/name#number".
var itemRef = regexp.MustCompile(`^[^/\s#]+/[^/\s#]+#[1-9][0-9]*$`)

// runList prints every Work Item, one per line. It reads state without
// taking the lock, so it works beside a running Tick.
func runList(args []string, stdout, stderr io.Writer) int {
	fs, configPath, err := flags("list", stderr)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	st, err := state.Read(filepath.Dir(*configPath))
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	if len(st.Items) == 0 {
		fmt.Fprintln(stdout, "No Work Items.")
		return 0
	}
	for _, w := range st.Items {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", w.ID, w.State, pausedBecause(&w), w.Title)
	}
	return 0
}

// runItem runs inspect, pause, resume or wake on one Work Item. pause and
// resume change only state.json; they never touch GitHub or the Workspace.
func runItem(cmd string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || !itemRef.MatchString(args[0]) {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	id := args[0]
	fs, configPath, err := flags(cmd, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	stateDir := filepath.Dir(*configPath)

	if cmd == "inspect" {
		return inspect(stateDir, id, stdout, stderr)
	}

	// Take the lock before anything else, so the command fails fast while
	// a Tick runs.
	store, err := state.Open(stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	defer store.Close()

	var w workflow.WorkItem
	switch cmd {
	case "pause":
		w, err = core.Pause(store, id)
	case "resume":
		w, err = core.Resume(store, id)
	case "wake":
		return wake(store, *configPath, id, stdout, stderr)
	}
	return report(w, err, stdout, stderr)
}

// wake carries out the item's pending Wake now, logging like a Tick does.
func wake(store *state.Store, configPath, id string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	logFile, err := logging.OpenFile(filepath.Dir(configPath))
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
	w, err := daemon.Wake(ctx, store, id)
	if err != nil {
		err = errors.New(log.Redact(err.Error()))
	}
	return report(w, err, stdout, stderr)
}

// report prints the item's new state, if it is tracked, and any error.
func report(w workflow.WorkItem, err error, stdout, stderr io.Writer) int {
	if w.ID != "" {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", w.ID, w.State, pausedBecause(&w))
	}
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	return 0
}

func inspect(stateDir, id string, stdout, stderr io.Writer) int {
	st, err := state.Read(stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	w := find(st, id)
	if w == nil {
		fmt.Fprintf(stderr, "work-agent: %s is not a tracked Work Item\n", id)
		return 1
	}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

func find(st workflow.State, id string) *workflow.WorkItem {
	for i := range st.Items {
		if st.Items[i].ID == id {
			return &st.Items[i]
		}
	}
	return nil
}

// pausedBecause says why w is Paused, or "-" when it is not.
func pausedBecause(w *workflow.WorkItem) string {
	if why := core.PausedBecause(*w); why != "" {
		return why
	}
	return "-"
}
