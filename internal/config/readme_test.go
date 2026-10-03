package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestREADMEConfigExampleLoads keeps the README's config.json example — the
// first json block under "## Configure" — one a new Operator can copy as is.
func TestREADMEConfigExampleLoads(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may have CRLF line endings.
	text := strings.ReplaceAll(string(readme), "\r\n", "\n")
	_, section, ok := strings.Cut(text, "\n## Configure\n")
	if !ok {
		t.Fatal("README has no ## Configure section")
	}
	_, block, ok := strings.Cut(section, "```json\n")
	if !ok {
		t.Fatal("## Configure has no json block")
	}
	example, _, _ := strings.Cut(block, "```")

	cfg, err := Load(write(t, example))
	if err != nil {
		t.Fatalf("the README's config example does not load: %v", err)
	}
	if cfg.GitHub.EligibilityLabel != "agent-ready" || cfg.Claude.EntrySkill != DefaultEntrySkill || cfg.QuietPeriod() != 5*time.Minute {
		t.Errorf("README example loaded as %+v", cfg)
	}
}
