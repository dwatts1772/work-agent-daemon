// Package workspace defines the WorkspaceBackend seam the daemon uses to
// create and Wake Workspaces. Orca implements it; Fake is an in-memory
// stand-in for tests.
package workspace

import "context"

// Workspace is the isolated checkout where Claude works on one Work Item. It
// is identified durably by Orca's identity key, its path and the
// daemon-owned Claude session ID (ADR-0002); terminal handles are never
// stored.
type Workspace struct {
	OrcaIdentityKey string `json:"orcaIdentityKey"`
	Path            string `json:"path"`
	Branch          string `json:"branch"`
	ClaudeSessionID string `json:"claudeSessionId"`
}

// CreateInput describes the Owned Issue a Workspace is created for.
type CreateInput struct {
	Repo  string
	Issue int
}

// AgentState is the live state of a Workspace's agent, read as a signal each
// Tick and never stored (ADR-0001).
type AgentState string

const (
	AgentWorking AgentState = "working"
	AgentWaiting AgentState = "waiting"
	AgentIdle    AgentState = "idle"
	AgentNone    AgentState = "none"
)

// Backend is the WorkspaceBackend seam: it creates and Wakes Workspaces.
type Backend interface {
	Available(ctx context.Context) (bool, error)
	CreateForIssue(ctx context.Context, in CreateInput) (Workspace, error)
	AgentState(ctx context.Context, ws Workspace) (AgentState, error)
	Wake(ctx context.Context, ws Workspace, prompt string) error
	Exists(ctx context.Context, ws Workspace) (bool, error)
}
