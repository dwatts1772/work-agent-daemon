package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestREADMEDocumentsEveryCommand keeps the README's CLI reference in step
// with the commands work-agent actually takes.
func TestREADMEDocumentsEveryCommand(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	var documented [][]string
	for line := range strings.SplitSeq(string(readme), "\n") {
		fields := strings.Fields(line)
		if i := slices.Index(fields, "#"); i >= 0 {
			fields = fields[:i]
		}
		if len(fields) > 1 && fields[0] == "work-agent" {
			documented = append(documented, fields)
		}
	}

	var stderr bytes.Buffer
	if code := run(nil, &bytes.Buffer{}, &stderr); code != 2 {
		t.Fatalf("run with no args exited %d, want the usage", code)
	}
	commands := 0
	for _, line := range strings.Split(stderr.String(), "\n")[1:] {
		command := strings.Fields(strings.ReplaceAll(line, "[--config path]", ""))
		if len(command) == 0 {
			continue
		}
		commands++
		if !slices.ContainsFunc(documented, func(d []string) bool { return slices.Equal(d, command) }) {
			t.Errorf("README does not document %q", strings.Join(command, " "))
		}
	}
	if commands == 0 {
		t.Fatalf("found no commands in the usage:\n%s", stderr.String())
	}
}
