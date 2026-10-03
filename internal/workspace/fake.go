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

func (f *Fake) Available(context.Context) (bool, error) { return !f.Unavailable, nil }

func (f *Fake) CreateForIssue(_ context.Context, in CreateInput) (Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Unavailable {
		return Workspace{}, fmt.Errorf("fake backend unavailable")
	}
	name := fmt.Sprintf("issue-%d", in.Issue)
	ws := Workspace{
		OrcaIdentityKey: in.Repo + "/" + name,
		Path:            "/fake/" + in.Repo + "/" + name,
		Branch:          name,
		ClaudeSessionID: fmt.Sprintf("fake-session-%d", len(f.Created)+1),
	}
	f.Created = append(f.Created, ws)
	return ws, nil
}

func (f *Fake) AgentState(_ context.Context, ws Workspace) (AgentState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.States[ws.OrcaIdentityKey]; ok {
		return s, nil
	}
	return AgentNone, nil
}

func (f *Fake) Wake(_ context.Context, ws Workspace, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Unavailable {
		return fmt.Errorf("fake backend unavailable")
	}
	f.Wakes = append(f.Wakes, FakeWake{ws, prompt})
	return nil
}

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
