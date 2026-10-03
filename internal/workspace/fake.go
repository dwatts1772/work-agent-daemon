package workspace

import (
	"context"
	"fmt"
	"sync"
)

// Fake is an in-memory Backend for tests. Set Unavailable to simulate Orca
// being unreachable.
type Fake struct {
	mu          sync.Mutex
	Unavailable bool
	Created     []Workspace
	Wakes       []FakeWake
	States      map[string]AgentState // keyed by OrcaIdentityKey
}

// FakeWake is one recorded Wake.
type FakeWake struct {
	Workspace Workspace
	Prompt    string
}

var _ Backend = (*Fake)(nil)

// Available reports whether the fake is reachable.
func (f *Fake) Available(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.Unavailable, nil
}

// CreateForIssue records and returns a made-up Workspace.
func (f *Fake) CreateForIssue(_ context.Context, in CreateInput) (Workspace, error) {
	return f.create(in.Repo, fmt.Sprintf("issue-%d", in.Issue))
}

// CreateForReview records and returns a made-up Review Workspace.
func (f *Fake) CreateForReview(_ context.Context, in ReviewInput) (Workspace, error) {
	return f.create(in.Repo, fmt.Sprintf("review-pr-%d", in.PR))
}

func (f *Fake) create(repo, name string) (Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Unavailable {
		return Workspace{}, fmt.Errorf("fake backend unavailable")
	}
	ws := Workspace{
		OrcaIdentityKey: repo + "/" + name,
		Path:            "/fake/" + repo + "/" + name,
		Branch:          name,
		ClaudeSessionID: fmt.Sprintf("fake-session-%d", len(f.Created)+1),
	}
	f.Created = append(f.Created, ws)
	return ws, nil
}

// AgentState returns States[ws.OrcaIdentityKey], or AgentNone.
func (f *Fake) AgentState(_ context.Context, ws Workspace) (AgentState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.States[ws.OrcaIdentityKey]; ok {
		return s, nil
	}
	return AgentNone, nil
}

// Wake records the Wake.
func (f *Fake) Wake(_ context.Context, ws Workspace, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Unavailable {
		return fmt.Errorf("fake backend unavailable")
	}
	f.Wakes = append(f.Wakes, FakeWake{ws, prompt})
	return nil
}

// Exists reports whether the fake created ws.
func (f *Fake) Exists(_ context.Context, ws Workspace) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Created {
		if c == ws {
			return true, nil
		}
	}
	return false, nil
}
