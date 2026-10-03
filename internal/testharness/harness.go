package testharness

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// Stubs is a directory of stub executables for one test.
type Stubs struct {
	Dir string
	// Paths maps "gh", "git", "orca" and "claude" to absolute stub paths,
	// in the shape of config.Config.Binaries.
	Paths map[string]string
}

var (
	buildOnce sync.Once
	builtStub string
	buildErr  error
)

// New compiles the stub program (once per test binary) and installs copies
// named gh, git, orca and claude into a fresh temp dir.
func New(t *testing.T) *Stubs {
	t.Helper()
	buildOnce.Do(build)
	if buildErr != nil {
		t.Fatalf("build stub: %v", buildErr)
	}
	src, err := os.ReadFile(builtStub)
	if err != nil {
		t.Fatal(err)
	}
	s := &Stubs{Dir: t.TempDir(), Paths: map[string]string{}}
	for _, name := range []string{"gh", "git", "orca", "claude"} {
		path := filepath.Join(s.Dir, name+exeSuffix())
		if err := os.WriteFile(path, src, 0o755); err != nil {
			t.Fatal(err)
		}
		s.Paths[name] = path
	}
	return s
}

func build() {
	dir, err := os.MkdirTemp("", "work-agent-stub-")
	if err != nil {
		buildErr = err
		return
	}
	builtStub = filepath.Join(dir, "stub"+exeSuffix())
	cmd := exec.Command("go", "build", "-o", builtStub, "github.com/dwatts1772/work-agent-daemon/internal/testharness/stub")
	if out, err := cmd.CombinedOutput(); err != nil {
		buildErr = &buildError{err, string(out)}
	}
}

type buildError struct {
	err    error
	output string
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.output }

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// SetFixture writes the GitHub world the stub gh simulates.
func (s *Stubs) SetFixture(t *testing.T, fx Fixture) {
	t.Helper()
	data, err := json.Marshal(fx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, FixtureFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// OrcaWorktrees returns the worktrees the stub orca has created, in order.
func (s *Stubs) OrcaWorktrees(t *testing.T) []OrcaWorktree {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Dir, OrcaWorktreesFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var wts []OrcaWorktree
	if err := json.Unmarshal(data, &wts); err != nil {
		t.Fatal(err)
	}
	return wts
}

// Calls returns every stub invocation so far, in order.
func (s *Stubs) Calls(t *testing.T) []Call {
	t.Helper()
	f, err := os.Open(filepath.Join(s.Dir, CallsFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var calls []Call
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c Call
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, c)
	}
	return calls
}
