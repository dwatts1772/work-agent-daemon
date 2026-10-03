package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	path, _ := configWith(t, testharness.Fixture{
		Tokens: map[string]string{operator: token},
		Users:  map[string]string{token: operator},
	})
	return path
}

// configWith writes a config whose binaries are stubs simulating fx.
func configWith(t *testing.T, fx testharness.Fixture) (string, *testharness.Stubs) {
	t.Helper()
	stubs := testharness.New(t)
	stubs.SetFixture(t, fx)
	return writeConfig(t, stubs.Paths), stubs
}

// writeConfig writes a config for the Operator with binaries, if any.
func writeConfig(t *testing.T, binaries map[string]string) string {
	t.Helper()
	cfg := map[string]any{
		"github": map[string]any{"account": operator, "repos": []string{"org/a"}, "eligibilityLabel": "agent-ready"},
	}
	if binaries != nil {
		cfg["binaries"] = binaries
	}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestASecondInstanceRefusesToStart(t *testing.T) {
	path := configFor(t)
	first, err := prepare(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(context.Background(), path, nil); !errors.Is(err, state.ErrAlreadyRunning) {
		t.Fatalf("second instance: err = %v, want ErrAlreadyRunning", err)
	}
	first.close()

	again, err := prepare(context.Background(), path, nil)
	if err != nil {
		t.Fatalf("start after the first instance quit: %v", err)
	}
	again.close()
}

func TestTheTrayAppRefusesToStartWhenGHIsNotLoggedInAsTheOperator(t *testing.T) {
	path, stubs := configWith(t, testharness.Fixture{Users: map[string]string{token: operator}})

	tr, err := prepare(context.Background(), path, nil)

	if err == nil {
		tr.close()
		t.Fatal("prepare succeeded, want refusal")
	}
	if !strings.Contains(err.Error(), operator) || !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("err = %v, want one naming the account and how to fix it", err)
	}
	assertNoIssueListing(t, stubs)
}

func TestTheTrayAppRefusesToStartWhenTheTokenBelongsToSomeoneElse(t *testing.T) {
	path, stubs := configWith(t, testharness.Fixture{
		Tokens: map[string]string{operator: token},
		Users:  map[string]string{token: "impostor"},
	})

	tr, err := prepare(context.Background(), path, nil)

	if err == nil {
		tr.close()
		t.Fatal("prepare succeeded, want refusal")
	}
	if !strings.Contains(err.Error(), "impostor") || !strings.Contains(err.Error(), operator) {
		t.Errorf("err = %v, want one naming both logins", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("err leaks the token: %v", err)
	}
	assertNoIssueListing(t, stubs)
}

func assertNoIssueListing(t *testing.T, stubs *testharness.Stubs) {
	t.Helper()
	for _, call := range stubs.Calls(t) {
		if len(call.Args) > 1 && call.Args[0] == "issue" {
			t.Errorf("listed issues despite failed Operator verification: %v", call.Args)
		}
	}
}
