package state

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
	"github.com/dwatts1772/work-agent-daemon/internal/workspace"
)

func sample() workflow.State {
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	return workflow.State{Items: []workflow.WorkItem{{
		ID:                "org/a#1",
		Kind:              workflow.KindOwnedIssue,
		State:             workflow.PendingWorkspace,
		Repo:              "org/a",
		Issue:             1,
		Title:             "Eligible in a",
		IssueURL:          "https://github.com/org/a/issues/1",
		Workspace:         &workspace.Workspace{OrcaIdentityKey: "k", Path: "/p", Branch: "issue-1", ClaudeSessionID: "uuid"},
		ProcessedEventIDs: []string{"org/a#1:assigned"},
		CreatedAt:         at,
		UpdatedAt:         at,
	}}}
}

func TestSavedStateSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(sample()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sample()) {
		t.Errorf("reloaded state =\n  %+v\nwant\n  %+v", got, sample())
	}

	read, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read, sample()) {
		t.Errorf("Read() differs from saved state")
	}
}

func TestMissingStateIsEmpty(t *testing.T) {
	dir := t.TempDir()

	got, err := Read(dir)

	if err != nil || len(got.Items) != 0 {
		t.Errorf("Read() = %+v, %v; want empty state", got, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("Read wrote to the state directory: %v", entries)
	}
}

func TestSaveReplacesStateAtomically(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(sample()); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(workflow.State{}); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load()
	if err != nil || len(got.Items) != 0 {
		t.Errorf("Load() = %+v, %v; want the second save", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json.tmp")); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}
}

func TestCorruptStateIsAnErrorNotEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Read(dir); err == nil {
		t.Error("Read() of corrupt state.json succeeded; it must not silently start empty")
	}
}

func TestSecondOpenFailsFastWhileLocked(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	second, err := Open(dir)
	if err == nil {
		second.Close()
		t.Fatal("second Open succeeded while the state directory was locked")
	}
	if !errors.Is(err, ErrLocked) {
		t.Errorf("err = %v, want ErrLocked", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("second Open took %v; it must fail fast", time.Since(start))
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := Open(dir)
	if err != nil {
		t.Fatalf("Open after release: %v", err)
	}
	third.Close()
}
