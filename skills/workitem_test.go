// Contract tests for the Entry Skill (skills/work-item). The skill is markdown
// read by Claude, never by the daemon; these tests pin the parts of it that
// must not drift: the default Routing table, the layer-merge rule, and the
// push/review policy.
package skills

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const skillDir = "work-item"

type routeKey struct{ reason, situation string }

// routingTable parses every Routing row (`| reason | situation | route |`) in a
// markdown file. Header and separator rows are skipped.
func routingTable(t *testing.T, path string) map[routeKey]string {
	t.Helper()
	rows := map[routeKey]string{}
	for line := range strings.SplitSeq(readText(t, path), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 3 {
			t.Fatalf("%s: routing row needs 3 cells: %q", path, line)
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		reason := strings.Trim(cells[0], "`")
		if reason == "Wake Reason" || strings.HasPrefix(reason, "-") {
			continue
		}
		rows[routeKey{reason, strings.Trim(cells[1], "`")}] = cells[2]
	}
	return rows
}

// mergeLayers applies the layer-merge rule stated in SKILL.md: layers in order,
// a row replaces any earlier row with the same Wake Reason and Situation,
// new rows are added.
func mergeLayers(layers ...map[routeKey]string) map[routeKey]string {
	out := map[routeKey]string{}
	for _, layer := range layers {
		maps.Copy(out, layer)
	}
	return out
}

func readSkill(t *testing.T) string {
	t.Helper()
	return readText(t, filepath.Join(skillDir, "SKILL.md"))
}

// readText reads a markdown file with LF line endings, whatever
// core.autocrlf did to the checkout.
func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func assertRoute(t *testing.T, table map[routeKey]string, reason, situation string, want ...string) {
	t.Helper()
	got, ok := table[routeKey{reason, situation}]
	if !ok {
		t.Errorf("no route for %s/%s", reason, situation)
		return
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s/%s routes to %q, want it to contain %q", reason, situation, got, w)
		}
	}
}

func TestDefaultRoutingCoversEveryWakeReason(t *testing.T) {
	table := routingTable(t, filepath.Join(skillDir, "routing.md"))

	assertRoute(t, table, "issue", "unclear", "/grill-with-docs")
	assertRoute(t, table, "issue", "large", "/grill-with-docs", "/to-spec", "/to-tickets")
	assertRoute(t, table, "issue", "implementable", "/implement")
	assertRoute(t, table, "feedback", "any", "address", "verify", "push (non-force)")
	assertRoute(t, table, "ci-failure", "any", "/diagnosing-bugs", "verify", "push (non-force)")
	assertRoute(t, table, "review", "first", "/code-review PR#<n>", "held for Operator approval")
	assertRoute(t, table, "review", "new-head", "/code-review", "new diff", "unresolved prior findings", "held for Operator approval")

	if len(table) != 7 {
		t.Errorf("default Routing has %d rows, want 7: %v", len(table), table)
	}
}

func TestOverridesChangeRoutingWithRepoWinning(t *testing.T) {
	defaultLayer := routingTable(t, filepath.Join(skillDir, "routing.md"))
	operator := routingTable(t, filepath.Join("testdata", "operator-routing.md"))
	repo := routingTable(t, filepath.Join("testdata", "repo-routing.md"))

	withOperator := mergeLayers(defaultLayer, operator)
	assertRoute(t, withOperator, "feedback", "any", "/operator-feedback")
	assertRoute(t, withOperator, "ci-failure", "any", "/operator-ci")

	withRepo := mergeLayers(defaultLayer, repo)
	assertRoute(t, withRepo, "feedback", "any", "/repo-feedback")
	assertRoute(t, withRepo, "issue", "docs-only", "edit the docs directly")

	all := mergeLayers(defaultLayer, operator, repo)
	assertRoute(t, all, "feedback", "any", "/repo-feedback")    // repo beats Operator
	assertRoute(t, all, "ci-failure", "any", "/operator-ci")    // Operator beats default
	assertRoute(t, all, "issue", "implementable", "/implement") // untouched rows inherit
	assertRoute(t, all, "issue", "docs-only", "edit the docs")  // new rows add
}

// The merge rule above is only as good as SKILL.md's statement of it: the
// three layers, in this order, merged row by row on Wake Reason + Situation.
func TestSkillStatesLayerOrderAndMergeRule(t *testing.T) {
	skill := readSkill(t)
	layers := []string{"routing.md", "~/.work-agent/routing.md", "docs/agents/work-item-routing.md"}
	last := -1
	for _, l := range layers {
		i := strings.Index(skill, "`"+l+"`")
		if i < 0 {
			t.Fatalf("SKILL.md does not name layer `%s`", l)
		}
		if i < last {
			t.Errorf("SKILL.md names layer `%s` out of order", l)
		}
		last = i
	}
	for _, phrase := range []string{"same Wake Reason and Situation", "repo override wins", "a specific Situation beats `any`"} {
		if !strings.Contains(skill, phrase) {
			t.Errorf("SKILL.md does not state %q", phrase)
		}
	}
}

// Every Entry Skill file may name a forbidden action only as a guardrail: on
// a line that also says "never". Anywhere else it reads as an instruction.
func TestForbiddenActionsAppearOnlyAsGuardrails(t *testing.T) {
	forbidden := []string{"--force", "--force-with-lease", "push -f", "push origin +", "gh pr ready", "gh pr merge", "--approve", "--request-changes"}
	files, err := filepath.Glob(filepath.Join(skillDir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		for n, line := range strings.Split(readText(t, f), "\n") {
			for _, bad := range forbidden {
				if strings.Contains(line, bad) && !strings.Contains(strings.ToLower(line), "never") {
					t.Errorf("%s:%d names %q outside a guardrail: %s", f, n+1, bad, line)
				}
			}
		}
	}
}

// ADR-0003 makes Review Workspaces unable to push; the skill states it too.
func TestSkillStatesPushPolicy(t *testing.T) {
	skill := readSkill(t)
	for _, want := range []string{
		"gh pr create --draft",                            // PRs open as drafts
		"Marking a PR ready for review is the Operator's", // ready is Operator-gated
		"Review Workspace never pushes",                   // review side is read-only
		"held for Operator approval",                      // findings wait for the Operator
		"exactly what the Operator approved",              // and only approved text is submitted
		"--force",                                         // the guardrail is spelled out
		"gh pr ready",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md push policy does not state %q", want)
		}
	}
}

func TestReReviewFocusesOnNewDiffAndUnresolvedPriorFindings(t *testing.T) {
	skill := readSkill(t)
	start := strings.Index(skill, "## 4. Re-review")
	end := strings.Index(skill, "## Push policy")
	if start < 0 || end < start {
		t.Fatal("SKILL.md has no Re-review section before the push policy")
	}
	section := skill[start:end]
	for _, want := range []string{
		"`new-head`",                     // selects the re-review route
		"headRefOid",                     // compared with the PR head on GitHub, not the stale local HEAD
		"work-item/review.md",            // held findings record the reviewed head
		"submitted review",               // ...or the Operator's GitHub review does
		"git reset --keep origin/pr/<n>", // Workspace moves to the new head
		"git diff <last-reviewed-sha>",   // the new diff only
		"unresolved prior findings",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Re-review section does not state %q", want)
		}
	}
}

// The repo root is a Claude Code plugin (and its own marketplace) whose
// skills/ directory carries the Entry Skill; the same folder links into
// ~/.claude/skills unchanged.
func TestRepoIsInstallableAsPlugin(t *testing.T) {
	var plugin struct{ Name string }
	readJSON(t, filepath.Join("..", ".claude-plugin", "plugin.json"), &plugin)
	if plugin.Name == "" {
		t.Error("plugin.json has no name")
	}

	var market struct {
		Plugins []struct{ Name, Source string }
	}
	readJSON(t, filepath.Join("..", ".claude-plugin", "marketplace.json"), &market)
	if len(market.Plugins) != 1 || market.Plugins[0].Name != plugin.Name || market.Plugins[0].Source != "./" {
		t.Errorf("marketplace.json must list plugin %q at ./, got %+v", plugin.Name, market.Plugins)
	}

	if !strings.HasPrefix(readSkill(t), "---\nname: work-item\n") {
		t.Error("SKILL.md frontmatter must open with name: work-item")
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// The contract tests above prove the rule is written down; this one proves
// Claude reads it that way. Opt-in: it calls the real `claude` CLI.
func TestLiveRoutingEval(t *testing.T) {
	if os.Getenv("WORK_AGENT_LIVE_EVAL") != "1" {
		t.Skip("set WORK_AGENT_LIVE_EVAL=1 to run against the real claude CLI")
	}
	file := func(label, path string) string {
		return "<file path=\"" + label + "\">\n" + readText(t, path) + "\n</file>\n"
	}
	base := file("work-item/SKILL.md", filepath.Join(skillDir, "SKILL.md")) +
		file("work-item/routing.md", filepath.Join(skillDir, "routing.md")) +
		file("~/.work-agent/routing.md", filepath.Join("testdata", "operator-routing.md"))
	const ask = "Apply only step 2 (Resolve Routing) of the skill above using exactly the layer files shown; any other layer does not exist. " +
		"For each Wake Reason/Situation below, reply with one line `<reason>/<situation>: <route>` copying the winning route cell verbatim, and nothing else.\n" +
		"feedback/any\nci-failure/any\nissue/implementable\nissue/docs-only\n"

	cases := []struct {
		name, prompt string
		want         map[string]string
	}{
		{"operator only", base + ask, map[string]string{
			"feedback/any": "/operator-feedback", "ci-failure/any": "/operator-ci", "issue/implementable": "/implement",
		}},
		{"operator and repo", base + file("docs/agents/work-item-routing.md", filepath.Join("testdata", "repo-routing.md")) + ask, map[string]string{
			"feedback/any": "/repo-feedback", "ci-failure/any": "/operator-ci", "issue/implementable": "/implement", "issue/docs-only": "edit the docs",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "claude", "-p", "--tools", "", "--no-session-persistence")
			cmd.Stdin = strings.NewReader(c.prompt)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("claude -p: %v", err)
			}
			got := map[string]string{}
			for line := range strings.SplitSeq(string(out), "\n") {
				key, route, ok := strings.Cut(strings.Trim(strings.TrimSpace(line), "`"), ": ")
				if ok {
					got[key] = route
				}
			}
			for key, want := range c.want {
				if !strings.Contains(got[key], want) {
					t.Errorf("%s resolved to %q, want %q\nfull reply:\n%s", key, got[key], want, out)
				}
			}
		})
	}
}
