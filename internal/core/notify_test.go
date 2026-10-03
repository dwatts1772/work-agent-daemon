package core_test

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/core"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/notify"
	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
	"github.com/dwatts1772/work-agent-daemon/internal/workflow"
)

// recorder records every Notification the daemon delivers.
type recorder struct{ got []notify.Notification }

func (r *recorder) Notify(n notify.Notification) error {
	r.got = append(r.got, n)
	return nil
}

// count is how many Notifications of kind were delivered for item.
func (r *recorder) count(kind notify.Kind, item string) int {
	n := 0
	for _, x := range r.got {
		if x.Kind == kind && x.Item == item {
			n++
		}
	}
	return n
}

// notified is a started daemon with a recording Notifier, Ticking against
// stubs whose world is fx.
type notified struct {
	t        *testing.T
	stubs    *testharness.Stubs
	fx       testharness.Fixture
	daemon   *core.Daemon
	stateDir string
	rec      *recorder
}

func newNotified(t *testing.T, fx testharness.Fixture) *notified {
	t.Helper()
	fx.Tokens = map[string]string{operator: token}
	fx.Users = map[string]string{token: operator}
	stubs := testharness.New(t)
	stubs.SetFixture(t, fx)
	cfg := config.Config{
		GitHub:   config.GitHub{Account: operator, Repos: []string{"org/a"}, EligibilityLabel: "agent-ready"},
		Claude:   config.Claude{EntrySkill: config.DefaultEntrySkill},
		Binaries: stubs.Paths,
	}
	daemon, err := core.Start(context.Background(), cfg, logging.New(io.Discard, io.Discard), nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	daemon.AddNotifier(rec)
	return &notified{t: t, stubs: stubs, fx: fx, daemon: daemon, stateDir: filepath.Join(t.TempDir(), "state"), rec: rec}
}

func (n *notified) set(change func(*testharness.Fixture)) {
	n.t.Helper()
	change(&n.fx)
	n.stubs.SetFixture(n.t, n.fx)
}

func (n *notified) tick() {
	n.t.Helper()
	store, err := state.Open(n.stateDir)
	if err != nil {
		n.t.Fatal(err)
	}
	defer store.Close()
	if _, err := n.daemon.Tick(context.Background(), store); err != nil {
		n.t.Fatal(err)
	}
}

func (n *notified) expect(kind notify.Kind, item string, want int) {
	n.t.Helper()
	if got := n.rec.count(kind, item); got != want {
		n.t.Fatalf("%d %s notifications for %q, want %d; all: %+v", got, kind, item, want, n.rec.got)
	}
}

func eligible() map[string][]testharness.Issue {
	return map[string][]testharness.Issue{"org/a": {
		{Number: 1, Title: "One", URL: "https://github.com/org/a/issues/1", Labels: []string{"agent-ready"}, Assignees: []string{operator}},
	}}
}

func orcaUp() *testharness.Orca {
	return &testharness.Orca{Repos: map[string]string{"repo-a": "org/a"}}
}

func TestOrcaUnavailableIsNotifiedOncePerOutage(t *testing.T) {
	n := newNotified(t, testharness.Fixture{Issues: eligible()})

	n.tick()
	n.tick()
	n.expect(notify.OrcaUnavailable, "", 1)

	n.set(func(fx *testharness.Fixture) { fx.Orca = orcaUp() })
	n.tick()
	n.set(func(fx *testharness.Fixture) { fx.Orca = nil })
	n.tick()
	n.expect(notify.OrcaUnavailable, "", 2)
}

func TestAWorkItemPausedIsNotifiedOnce(t *testing.T) {
	n := newNotified(t, testharness.Fixture{Issues: eligible(), Orca: orcaUp()})
	n.tick()
	n.expect(notify.Paused, "org/a#1", 0)

	n.set(func(fx *testharness.Fixture) { fx.Issues["org/a"][0].Labels = nil })
	n.tick()
	n.tick()
	n.expect(notify.Paused, "org/a#1", 1)
}

func TestAFailedWorkItemIsNotifiedOnce(t *testing.T) {
	// Orca has no repo for org/a, so creating the Workspace keeps failing.
	n := newNotified(t, testharness.Fixture{Issues: eligible(), Orca: &testharness.Orca{}})
	for range workflow.MaxActionFailures + 2 {
		n.tick()
	}
	n.expect(notify.Failed, "org/a#1", 1)
}

func TestAnAgentWaitingOnTheOperatorIsNotifiedOncePerWait(t *testing.T) {
	n := newNotified(t, testharness.Fixture{Issues: eligible(), Orca: orcaUp()})
	n.tick() // creates the Workspace and Wakes Claude

	waiting := func(states ...string) {
		n.set(func(fx *testharness.Fixture) { fx.Orca.AgentStates = map[string][]string{"issue-1": states} })
	}
	waiting("waiting")
	n.tick()
	n.tick()
	n.expect(notify.AgentWaiting, "org/a#1", 1)

	waiting("working")
	n.tick()
	waiting("waiting")
	n.tick()
	n.expect(notify.AgentWaiting, "org/a#1", 2)
}

func TestReadyToMergeIsNotifiedOnce(t *testing.T) {
	n := newNotified(t, testharness.Fixture{Issues: eligible(), Orca: orcaUp()})
	store, err := state.Open(n.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Save(workflow.State{Items: []workflow.WorkItem{{
		ID: "org/a#1", Kind: workflow.KindOwnedIssue, State: workflow.ReadyToMerge,
		Repo: "org/a", Issue: 1, Title: "One",
	}}})
	store.Close()
	if err != nil {
		t.Fatal(err)
	}

	n.tick()
	n.tick()
	n.expect(notify.ReadyToMerge, "org/a#1", 1)
}

func TestAnAgentStillWaitingAfterAnOrcaOutageIsNotNotifiedAgain(t *testing.T) {
	n := newNotified(t, testharness.Fixture{Issues: eligible(), Orca: orcaUp()})
	n.tick()
	n.set(func(fx *testharness.Fixture) { fx.Orca.AgentStates = map[string][]string{"issue-1": {"waiting"}} })
	n.tick()

	up := n.fx.Orca
	n.set(func(fx *testharness.Fixture) { fx.Orca = nil })
	n.tick()
	n.set(func(fx *testharness.Fixture) { fx.Orca = up })
	n.tick()

	n.expect(notify.AgentWaiting, "org/a#1", 1)
	n.expect(notify.OrcaUnavailable, "", 1)
}
