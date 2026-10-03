package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadReadsOperatorReposLabelAndBinaryOverrides(t *testing.T) {
	bin := func(name string) string { return filepath.Join(t.TempDir(), name) }
	gh, git, orca, claude := bin("gh"), bin("git"), bin("orca"), bin("claude")
	bins, _ := json.Marshal(map[string]string{"gh": gh, "git": git, "orca": orca, "claude": claude})
	path := write(t, `{
		"github": {
			"account": "dwatts1772",
			"repos": ["org/a", "org/b"],
			"eligibilityLabel": "agent-ready",
			"pollIntervalSeconds": 45
		},
		"binaries": `+string(bins)+`,
		"capacity": {"ownedIssueSlots": 1}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	want := Config{
		GitHub: GitHub{
			Account:          "dwatts1772",
			Repos:            []string{"org/a", "org/b"},
			EligibilityLabel: "agent-ready",
		},
		Claude:   Claude{EntrySkill: "/work-item"},
		Binaries: map[string]string{"gh": gh, "git": git, "orca": orca, "claude": claude},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got %+v\nwant %+v", cfg, want)
	}
}

func TestLoadRejectsIncompleteConfig(t *testing.T) {
	cases := map[string]struct{ body, wantErr string }{
		"no account":           {`{"github":{"repos":["o/r"],"eligibilityLabel":"l"}}`, "github.account"},
		"no repos":             {`{"github":{"account":"a","eligibilityLabel":"l"}}`, "github.repos"},
		"bad repo":             {`{"github":{"account":"a","repos":["noslash"],"eligibilityLabel":"l"}}`, "noslash"},
		"no label":             {`{"github":{"account":"a","repos":["o/r"]}}`, "github.eligibilityLabel"},
		"bad json":             {`{`, "config"},
		"unknown bin":          {`{"github":{"account":"a","repos":["o/r"],"eligibilityLabel":"l"},"binaries":{"sh":"sh"}}`, "sh"},
		"relative bin":         {`{"github":{"account":"a","repos":["o/r"],"eligibilityLabel":"l"},"binaries":{"gh":"gh"}}`, "absolute"},
		"shell in entry skill": {`{"github":{"account":"a","repos":["o/r"],"eligibilityLabel":"l"},"claude":{"entrySkill":"/x; rm -rf ~"}}`, "claude.entrySkill"},
		"spaced entry skill":   {`{"github":{"account":"a","repos":["o/r"],"eligibilityLabel":"l"},"claude":{"entrySkill":"/a b"}}`, "claude.entrySkill"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, c.body))
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want mention of %q", err, c.wantErr)
			}
		})
	}
}

func TestLoadReportsMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatal("want error for missing file")
	}
}

func TestLoadReadsACustomEntrySkill(t *testing.T) {
	cfg, err := Load(write(t, `{"github":{"account":"a","repos":["o/r"],"eligibilityLabel":"l"},"claude":{"entrySkill":"/my-plugin:entry"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Claude.EntrySkill != "/my-plugin:entry" {
		t.Errorf("EntrySkill = %q", cfg.Claude.EntrySkill)
	}
}
