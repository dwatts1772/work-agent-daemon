package core

import (
	"os/exec"
	"strings"
	"testing"
)

// ADR-0004: the core is embedded by the headless CLI, so nothing under
// internal/ may depend on Wails, directly or transitively.
func TestNoInternalPackageDependsOnWails(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/dwatts1772/work-agent-daemon/internal/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.Contains(pkg, "wailsapp") {
			t.Errorf("internal/ depends on %s", pkg)
		}
	}
}
