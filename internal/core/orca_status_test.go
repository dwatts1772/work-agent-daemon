package core_test

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/dwatts1772/work-agent-daemon/internal/config"
	"github.com/dwatts1772/work-agent-daemon/internal/core"
	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
)

const (
	operator = "dwatts1772"
	token    = "gho_OperatorToken0123456789abcdefABCDEF"
)

func TestTheStatusFollowsOrcaGoingAwayAndComingBack(t *testing.T) {
	stubs := testharness.New(t)
	fx := testharness.Fixture{
		Tokens: map[string]string{operator: token},
		Users:  map[string]string{token: operator},
		Orca:   &testharness.Orca{},
	}
	stubs.SetFixture(t, fx)
	cfg := config.Config{
		GitHub:   config.GitHub{Account: operator, Repos: []string{"org/a"}, EligibilityLabel: "agent-ready"},
		Binaries: stubs.Paths,
	}
	log := logging.New(io.Discard, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	daemon, err := core.Start(ctx, cfg, log, nil)
	if err != nil {
		t.Fatal(err)
	}

	statuses := make(chan core.Status, 100)
	loop := core.NewLoop(filepath.Join(t.TempDir(), "state"), time.Hour, daemon, log, func(s core.Status) { statuses <- s })
	go loop.Run(ctx)

	expect := func(want core.Status) {
		t.Helper()
		select {
		case got := <-statuses:
			if got != want {
				t.Fatalf("status = %s, want %s", got, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no status reported, want %s", want)
		}
	}

	expect(core.StatusOK)

	running := fx.Orca
	fx.Orca = nil // Orca quits
	stubs.SetFixture(t, fx)
	loop.TickNow()
	expect(core.StatusOrcaUnavailable)

	fx.Orca = running
	stubs.SetFixture(t, fx)
	loop.TickNow()
	expect(core.StatusOK)
}
