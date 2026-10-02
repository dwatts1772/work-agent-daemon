package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const token = "gho_0123456789abcdefABCDEF0123456789abcd"

func TestRegisteredSecretsNeverReachEitherSink(t *testing.T) {
	var console, jsonl bytes.Buffer
	log := New(&console, &jsonl)
	log.AddSecret("s3cret-value")

	log.Info("ran s3cret-value",
		"arg", "x=s3cret-value",
		"args", []string{"a", "s3cret-value"},
		"err", errors.New("bad credentials: s3cret-value"),
	)
	log.With("ctx", "s3cret-value").WithGroup("g").Error("boom", "k", "s3cret-value")

	for name, out := range map[string]string{"console": console.String(), "jsonl": jsonl.String()} {
		if strings.Contains(out, "s3cret-value") {
			t.Errorf("%s output leaks secret:\n%s", name, out)
		}
		if !strings.Contains(out, Redacted) {
			t.Errorf("%s output has no redaction marker:\n%s", name, out)
		}
	}
}

func TestGitHubTokenShapesAreRedactedEvenWhenUnregistered(t *testing.T) {
	var console, jsonl bytes.Buffer
	log := New(&console, &jsonl)

	log.Warn("stderr", "text", "token "+token+" rejected")

	if strings.Contains(console.String()+jsonl.String(), token) {
		t.Errorf("unregistered token leaked:\n%s%s", console.String(), jsonl.String())
	}
}

func TestJSONLIsOneObjectPerLine(t *testing.T) {
	var console, jsonl bytes.Buffer
	log := New(&console, &jsonl)

	log.Info("exec", "bin", "gh", "args", []string{"issue", "list"})
	log.Info("tick", "issues", 2)

	lines := strings.Split(strings.TrimSpace(jsonl.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), jsonl.String())
	}
	var rec struct {
		Level string   `json:"level"`
		Msg   string   `json:"msg"`
		Args  []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Msg != "exec" || rec.Level != "INFO" || len(rec.Args) != 2 {
		t.Errorf("unexpected record %+v", rec)
	}
}
