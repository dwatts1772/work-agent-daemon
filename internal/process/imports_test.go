package process

import (
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/dwatts1772/work-agent-daemon"

// Check only holds if every child process goes through Runner.Run: no other
// file of the packages built into the daemon's binaries, for any OS or build
// tag, may import os/exec. Test files and the test harness are not built into
// them.
func TestRunnerIsTheOnlyDaemonFileThatImportsOsExec(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}|{{.Dir}}|{{join .GoFiles \",\"}},{{join .CgoFiles \",\"}},{{join .IgnoredGoFiles \",\"}}", module+"/cmd/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	var importers []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		pkg, rest, _ := strings.Cut(strings.TrimSpace(line), "|")
		dir, files, _ := strings.Cut(rest, "|")
		if pkg != module && !strings.HasPrefix(pkg, module+"/") {
			continue
		}
		for name := range strings.SplitSeq(files, ",") {
			if name == "" {
				continue
			}
			path := filepath.Join(dir, name)
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				if p, _ := strconv.Unquote(imp.Path.Value); p == "os/exec" {
					importers = append(importers, pkg+"/"+name)
				}
			}
		}
	}
	want := module + "/internal/process/runner.go"
	if len(importers) != 1 || importers[0] != want {
		t.Errorf("files importing os/exec = %q, want only %s", importers, want)
	}
}
