package workflow

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The reducer must stay pure: it may import only these packages, and must
// not read the clock (time arrives in Event.ObservedAt).
var allowedImports = []string{
	"slices",
	"strconv",
	"time",
	"github.com/dwatts1772/work-agent-daemon/internal/workspace", // types only
}

func TestReducerHasNoIO(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, imp := range strings.Fields(string(out)) {
		if !slices.Contains(allowedImports, imp) {
			t.Errorf("workflow imports %s; the reducer must do no I/O", imp)
		}
	}

	src, err := os.ReadFile("workflow.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "time.Now") {
		t.Error("workflow.go reads the clock; take time from the Event instead")
	}
}
