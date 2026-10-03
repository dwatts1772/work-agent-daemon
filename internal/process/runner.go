// Package process runs the daemon's child processes: only allowlisted
// binaries and subcommands (see Check), always by absolute path and with an
// argument array — never through a shell.
package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dwatts1772/work-agent-daemon/internal/logging"
)

// Binaries are the executables the daemon may run.
var Binaries = []string{"gh", "git", "orca", "claude"}

// DefaultSearchDirs are the usual install locations of the Binaries, searched
// after PATH because login items and startup entries get a minimal PATH.
func DefaultSearchDirs() []string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		return []string{
			filepath.Join(os.Getenv("ProgramFiles"), "GitHub CLI"),
			filepath.Join(os.Getenv("ProgramFiles"), "Git", "cmd"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "orca", "resources", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(os.Getenv("APPDATA"), "npm"),
		}
	}
	return []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		filepath.Join(home, ".local", "bin"),
	}
}

// ResolveBinaries finds an absolute path for each of the Binaries: the
// override if one is configured (it must exist), else PATH, else searchDirs.
// Binaries that cannot be found are left out; running one fails with a hint.
func ResolveBinaries(overrides map[string]string, searchDirs []string) (map[string]string, error) {
	resolved := map[string]string{}
	for _, name := range Binaries {
		file := lookupName(name)
		if path, ok := overrides[name]; ok {
			if _, err := os.Stat(path); err != nil {
				return nil, fmt.Errorf("binaries.%s: %w", name, err)
			}
			if file != name && !strings.EqualFold(filepath.Base(path), file) {
				return nil, fmt.Errorf("binaries.%s: %q must be %s", name, path, file)
			}
			resolved[name] = path
			continue
		}
		if path, err := exec.LookPath(file); err == nil {
			if abs, err := filepath.Abs(path); err == nil {
				resolved[name] = abs
				continue
			}
		}
		for _, dir := range searchDirs {
			if path, err := exec.LookPath(filepath.Join(dir, file)); err == nil {
				resolved[name] = path
				break
			}
		}
	}
	return resolved, nil
}

// lookupName is the file to look for when resolving name. On Windows orca
// must be orca.exe: the orca.cmd shim installed beside it refuses to forward
// message bodies, and PATH lookup could otherwise find the shim first.
func lookupName(name string) string {
	if runtime.GOOS == "windows" && name == "orca" {
		return "orca.exe"
	}
	return name
}

// Runner executes allowlisted commands against resolved binaries.
type Runner struct {
	binaries map[string]string
	log      *logging.Logger
}

// NewRunner returns a Runner over binaries, as returned by ResolveBinaries.
func NewRunner(binaries map[string]string, log *logging.Logger) *Runner {
	return &Runner{binaries: binaries, log: log}
}

// Run executes bin with args and returns its stdout. env entries ("K=V") are
// added to the child's environment. GitHub tokens inherited from the daemon's
// own environment are never passed on: a child only gets a token when the
// caller supplies one explicitly. git always runs with GIT_TERMINAL_PROMPT=0.
func (r *Runner) Run(ctx context.Context, bin string, args []string, env ...string) ([]byte, error) {
	err := Check(bin, args)
	if err == nil {
		err = checkGHToken(bin, args, env)
	}
	if err != nil {
		r.log.Error("exec refused", "bin", bin, "args", args, "err", err)
		return nil, err
	}
	path, ok := r.binaries[bin]
	if !ok {
		return nil, fmt.Errorf("%s was not found on PATH; set binaries.%s in config", bin, bin)
	}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = childEnv(bin, env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	r.log.Info("exec", "bin", bin, "args", args)
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		r.log.Warn("exec failed", "bin", bin, "args", args, "err", err, "stderr", msg)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && msg != "" {
			return stdout.Bytes(), fmt.Errorf("%s %s: %s", bin, strings.Join(args, " "), msg)
		}
		return stdout.Bytes(), fmt.Errorf("%s %s: %w", bin, strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// checkGHToken requires every gh call except `gh auth token` (which fetches
// the token) to carry an explicit GH_TOKEN, so gh never falls back to its
// active account.
func checkGHToken(bin string, args, env []string) error {
	if bin != "gh" || (len(args) >= 2 && args[0] == "auth" && args[1] == "token") {
		return nil
	}
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "GH_TOKEN="); ok && v != "" {
			return nil
		}
	}
	return fmt.Errorf("gh %s: refusing to run without an explicit GH_TOKEN", strings.Join(args, " "))
}

// tokenVars are stripped from the inherited environment.
var tokenVars = []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}

func childEnv(bin string, extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if isTokenVar(key) || (bin == "git" && strings.EqualFold(key, "GIT_TERMINAL_PROMPT")) {
			continue
		}
		env = append(env, kv)
	}
	if bin == "git" {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	return append(env, extra...)
}

func isTokenVar(key string) bool {
	for _, v := range tokenVars {
		// Environment variable names are case-insensitive on Windows.
		if strings.EqualFold(key, v) {
			return true
		}
	}
	return false
}
