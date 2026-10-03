package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/state"
	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
)

const (
	operator = "dwatts1772"
	token    = "gho_OperatorToken0123456789abcdefABCDEF"
)

func configFor(t *testing.T) string {
	t.Helper()
	stubs := testharness.New(t)
	stubs.SetFixture(t, testharness.Fixture{
		Tokens: map[string]string{operator: token},
		Users:  map[string]string{token: operator},
	})
	data, _ := json.Marshal(map[string]any{
		"github":   map[string]any{"account": operator, "repos": []string{"org/a"}, "eligibilityLabel": "agent-ready"},
		"binaries": stubs.Paths,
	})
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestASecondInstanceRefusesToStart(t *testing.T) {
	path := configFor(t)
	first, err := prepare(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(context.Background(), path); !errors.Is(err, state.ErrAlreadyRunning) {
		t.Fatalf("second instance: err = %v, want ErrAlreadyRunning", err)
	}
	first.close()

	again, err := prepare(context.Background(), path)
	if err != nil {
		t.Fatalf("start after the first instance quit: %v", err)
	}
	again.close()
}
