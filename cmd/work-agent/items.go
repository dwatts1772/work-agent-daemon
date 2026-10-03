package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"time"

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

// runItem runs inspect, pause or resume on one Work Item. pause and resume
// change only state.json; they never touch GitHub or the Workspace.
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

	store, err := state.Open(stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	defer store.Close()
	current, err := store.Load()
	if err != nil {
		fmt.Fprintln(stderr, "work-agent:", err)
		return 1
	}
	change := workflow.PauseByOperator
	if cmd == "resume" {
		change = workflow.ResumeByOperator
	}
	next, actions, found := change(current, id, time.Now().UTC())
	if !found {
		fmt.Fprintf(stderr, "work-agent: %s is not a tracked Work Item\n", id)
		return 1
	}
	if len(actions) > 0 {
		if err := store.Save(next); err != nil {
			fmt.Fprintln(stderr, "work-agent: save state:", err)
			return 1
		}
	}
	w := find(next, id)
	fmt.Fprintf(stdout, "%s\t%s\t%s\n", w.ID, w.State, pausedBecause(w))
	if cmd == "resume" && w.Pause != nil && w.Pause.NotEligible {
		fmt.Fprintf(stderr, "work-agent: %s is still Paused: it is not Eligible (add the Eligibility Label and assign it to the Operator)\n", id)
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
	switch {
	case w.Pause == nil:
		return "-"
	case w.Pause.ByOperator && w.Pause.NotEligible:
		return "paused by Operator, not Eligible"
	case w.Pause.ByOperator:
		return "paused by Operator"
	default:
		return "not Eligible"
	}
}
