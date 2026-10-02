package process_test

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dwatts1772/work-agent-daemon/internal/logging"
	"github.com/dwatts1772/work-agent-daemon/internal/process"
	"github.com/dwatts1772/work-agent-daemon/internal/testharness"
)

func newRunner(t *testing.T) (*process.Runner, *testharness.Stubs) {
	t.Helper()
	stubs := testharness.New(t)
	return process.NewRunner(stubs.Paths, logging.New(io.Discard, io.Discard)), stubs
}

func TestRunExecutesTheConfiguredBinary(t *testing.T) {
	r, stubs := newRunner(t)

	out, err := r.Run(context.Background(), "orca", []string{"status", "--json"})
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSpace(string(out)) != "stub orca" {
		t.Errorf("stdout = %q", out)
	}
	calls := stubs.Calls(t)
	if len(calls) != 1 || calls[0].Bin != "orca" || strings.Join(calls[0].Args, " ") != "status --json" {
		t.Errorf("calls = %+v", calls)
	}
}

func TestRunRefusesDisallowedCommandsWithoutStartingAProcess(t *testing.T) {
	r, stubs := newRunner(t)

	for _, c := range []struct {
		bin  string
		args []string
	}{
		{"gh", []string{"pr", "merge", "4"}},
		{"git", []string{"push", "--force"}},
		{"git", []string{"push", "origin", "+main"}},
		{"git", []string{"push", "--force-w"}},
		{"gh", []string{"api", "-iXPUT", "repos/o/r/pulls/4/merge"}},
		{"gh", []string{"repo", "delete", "o/r"}},
	} {
		if _, err := r.Run(context.Background(), c.bin, c.args); err == nil {
			t.Errorf("Run(%s %v) succeeded, want refusal", c.bin, c.args)
		}
	}
	if calls := stubs.Calls(t); len(calls) != 0 {
		t.Errorf("refused commands still ran: %+v", calls)
	}
}

func TestRunNeverPassesInheritedGitHubTokensToChildren(t *testing.T) {
	t.Setenv("GH_TOKEN", "inherited-gh")
	t.Setenv("GITHUB_TOKEN", "inherited-github")
	r, stubs := newRunner(t)

	if _, err := r.Run(context.Background(), "orca", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), "claude", nil, "GH_TOKEN=explicit"); err != nil {
		t.Fatal(err)
	}

	calls := stubs.Calls(t)
	if _, ok := calls[0].Env["GH_TOKEN"]; ok {
		t.Errorf("inherited GH_TOKEN leaked: %+v", calls[0].Env)
	}
	if _, ok := calls[0].Env["GITHUB_TOKEN"]; ok {
		t.Errorf("inherited GITHUB_TOKEN leaked: %+v", calls[0].Env)
	}
	if calls[1].Env["GH_TOKEN"] != "explicit" {
		t.Errorf("explicit GH_TOKEN not passed: %+v", calls[1].Env)
	}
}

func TestRunRefusesGHCallsThatDoNotCarryAToken(t *testing.T) {
	r, stubs := newRunner(t)
	stubs.SetFixture(t, testharness.Fixture{})

	_, err := r.Run(context.Background(), "gh", []string{"issue", "list", "--repo", "o/r"})

	if err == nil || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Errorf("err = %v, want refusal naming GH_TOKEN", err)
	}
	if calls := stubs.Calls(t); len(calls) != 0 {
		t.Errorf("tokenless gh call still ran: %+v", calls)
	}
}

func TestRunDisablesGitTerminalPrompts(t *testing.T) {
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	r, stubs := newRunner(t)

	if _, err := r.Run(context.Background(), "git", []string{"status"}); err != nil {
		t.Fatal(err)
	}

	if got := stubs.Calls(t)[0].Env["GIT_TERMINAL_PROMPT"]; got != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q, want 0", got)
	}
}

func TestRunReportsStderrOnFailure(t *testing.T) {
	r, stubs := newRunner(t)
	stubs.SetFixture(t, testharness.Fixture{})

	_, err := r.Run(context.Background(), "gh", []string{"auth", "token", "--user", "nobody"})

	if err == nil || !strings.Contains(err.Error(), "no oauth token found") {
		t.Errorf("err = %v, want stderr in message", err)
	}
}

func TestRunFailsClearlyWhenBinaryWasNotResolved(t *testing.T) {
	r := process.NewRunner(map[string]string{}, logging.New(io.Discard, io.Discard))

	_, err := r.Run(context.Background(), "claude", []string{"--version"})

	if err == nil || !strings.Contains(err.Error(), "binaries.claude") {
		t.Errorf("err = %v, want hint to set binaries.claude", err)
	}
}

func TestResolveBinariesPrefersOverrides(t *testing.T) {
	stubs := testharness.New(t)
	t.Setenv("PATH", t.TempDir())

	got, err := process.ResolveBinaries(map[string]string{"gh": stubs.Paths["gh"]}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got["gh"] != stubs.Paths["gh"] {
		t.Errorf("gh = %q, want override %q", got["gh"], stubs.Paths["gh"])
	}
	if _, ok := got["claude"]; ok {
		t.Errorf("claude resolved to %q from an empty PATH", got["claude"])
	}
}

func TestResolveBinariesPrefersPathOverSearchDirs(t *testing.T) {
	onPath, inSearchDir := testharness.New(t), testharness.New(t)
	t.Setenv("PATH", onPath.Dir)

	got, err := process.ResolveBinaries(nil, []string{inSearchDir.Dir})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.EqualFold(got["gh"], onPath.Paths["gh"]) {
		t.Errorf("gh = %q, want %q", got["gh"], onPath.Paths["gh"])
	}
	if !filepath.IsAbs(got["git"]) {
		t.Errorf("git = %q, want absolute path", got["git"])
	}
}

func TestResolveBinariesFallsBackToSearchDirs(t *testing.T) {
	stubs := testharness.New(t)
	t.Setenv("PATH", t.TempDir())

	got, err := process.ResolveBinaries(nil, []string{stubs.Dir})
	if err != nil {
		t.Fatal(err)
	}

	if got["orca"] != stubs.Paths["orca"] {
		t.Errorf("orca = %q, want %q", got["orca"], stubs.Paths["orca"])
	}
}

func TestResolveBinariesRejectsMissingOverride(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gh.exe")

	_, err := process.ResolveBinaries(map[string]string{"gh": missing}, nil)

	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("err = %v, want mention of %s", err, missing)
	}
}
