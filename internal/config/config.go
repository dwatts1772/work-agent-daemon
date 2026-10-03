// Package config loads the daemon's config.json.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Config is the subset of config.json the daemon currently uses. Unknown
// keys are ignored so the file can carry settings for later features.
type Config struct {
	GitHub GitHub `json:"github"`
	Claude Claude `json:"claude"`
	// Binaries optionally overrides where gh, git, orca and claude live, for
	// login items whose minimal PATH cannot find them. Paths must be absolute.
	Binaries map[string]string `json:"binaries,omitempty"`
}

type GitHub struct {
	// Account is the Operator's GitHub login. Every gh call runs as it.
	Account string `json:"account"`
	// Repos is the allowlist of "owner/name" repos the daemon watches.
	Repos            []string `json:"repos"`
	EligibilityLabel string   `json:"eligibilityLabel"`
}

type Claude struct {
	// EntrySkill is the skill every Wake invokes; it defaults to
	// DefaultEntrySkill.
	EntrySkill string `json:"entrySkill"`
}

// DefaultEntrySkill is the Entry Skill shipped in this repo.
const DefaultEntrySkill = "/work-item"

// entrySkillPattern keeps the Entry Skill one shell-inert word, because it
// is typed into the Workspace terminal as part of the Wake command.
var entrySkillPattern = regexp.MustCompile(`^/?[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// DefaultPath is ~/.work-agent/config.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".work-agent", "config.json"), nil
}

// Load reads and validates the config file at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.Claude.EntrySkill == "" {
		cfg.Claude.EntrySkill = DefaultEntrySkill
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.GitHub.Account == "" {
		return fmt.Errorf("github.account is required")
	}
	if len(c.GitHub.Repos) == 0 {
		return fmt.Errorf("github.repos must list at least one repo")
	}
	for _, r := range c.GitHub.Repos {
		owner, name, ok := strings.Cut(r, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			return fmt.Errorf("github.repos entry %q is not owner/name", r)
		}
	}
	if c.GitHub.EligibilityLabel == "" {
		return fmt.Errorf("github.eligibilityLabel is required")
	}
	if !entrySkillPattern.MatchString(c.Claude.EntrySkill) {
		return fmt.Errorf("claude.entrySkill %q must be a single skill name such as %s", c.Claude.EntrySkill, DefaultEntrySkill)
	}
	for name, path := range c.Binaries {
		switch name {
		case "gh", "git", "orca", "claude":
		default:
			return fmt.Errorf("binaries.%s: unknown binary", name)
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("binaries.%s: %q must be an absolute path", name, path)
		}
	}
	return nil
}
