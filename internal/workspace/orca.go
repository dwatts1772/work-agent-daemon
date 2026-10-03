package workspace

import (
	"context"
	"encoding/json"

	"github.com/dwatts1772/work-agent-daemon/internal/process"
)

// Orca talks to the Orca CLI. So far it only probes availability; the rest
// of the Backend arrives with Workspace creation.
type Orca struct {
	runner *process.Runner
}

// NewOrca returns an Orca that runs the orca binary through runner.
func NewOrca(runner *process.Runner) *Orca { return &Orca{runner: runner} }

// Available reports whether an Orca runtime is reachable. It never launches
// Orca; any failure to ask counts as unavailable.
func (o *Orca) Available(ctx context.Context) bool {
	out, err := o.runner.Run(ctx, "orca", []string{"status", "--json"})
	if err != nil {
		return false
	}
	var status struct {
		OK     bool `json:"ok"`
		Result struct {
			Runtime struct {
				Reachable bool `json:"reachable"`
			} `json:"runtime"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return false
	}
	return status.OK && status.Result.Runtime.Reachable
}
