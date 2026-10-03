package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
			"pollIntervalSeconds": 30
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
			Account:             "dwatts1772",
			Repos:               []string{"org/a", "org/b"},
			EligibilityLabel:    "agent-ready",
			PollIntervalSeconds: 30,
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
		"bad interval":         {`{"github":{"account":"a","repos":["o/r"],"eligibilityLabel":"l","pollIntervalSeconds":-1}}`, "github.pollIntervalSeconds"},
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

func TestThePollIntervalDefaultsTo45Seconds(t *testing.T) {
	cfg, err := Load(write(t, `{"github":{"account":"a","repos":["o/r"],"eligibilityLabel":"l"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.PollInterval(); got != 45*time.Second {
		t.Errorf("PollInterval = %v, want 45s", got)
	}
}

func TestFeedbackSettings(t *testing.T) {
	cfg, err := Load(write(t, `{"github":{"account":"a","repos":["o/r"],"eligibilityLabel":"l"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.QuietPeriod(); got != 5*time.Minute {
		t.Errorf("QuietPeriod = %v, want the 5 minute default", got)
	}

	cfg, err = Load(write(t, `{"github":{"account":"a","repos":["o/r"],"eligibilityLabel":"l","quietPeriodMinutes":2,"feedbackBots":["coderabbitai[bot]"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.QuietPeriod(); got != 2*time.Minute {
		t.Errorf("QuietPeriod = %v, want 2m", got)
	}
	if !reflect.DeepEqual(cfg.GitHub.FeedbackBots, []string{"coderabbitai[bot]"}) {
		t.Errorf("FeedbackBots = %q", cfg.GitHub.FeedbackBots)
	}

	if _, err := Load(write(t, `{"github":{"account":"a","repos":["o/r"],"eligibilityLabel":"l","quietPeriodMinutes":-1}}`)); err == nil || !strings.Contains(err.Error(), "github.quietPeriodMinutes") {
		t.Errorf("err = %v, want a negative Quiet Period rejected", err)
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

func TestDesktopNotificationsAreOnUnlessDisabled(t *testing.T) {
	const base = `"github": {"account": "dwatts1772", "repos": ["org/a"], "eligibilityLabel": "agent-ready"}`
	for _, tc := range []struct {
		name, notify string
		want         bool
	}{
		{"absent", ``, true},
		{"enabled", `, "notify": {"desktop": true}`, true},
		{"disabled", `, "notify": {"desktop": false}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(write(t, `{`+base+tc.notify+`}`))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.DesktopNotifications(); got != tc.want {
				t.Errorf("DesktopNotifications() = %v, want %v", got, tc.want)
			}
		})
	}
}
