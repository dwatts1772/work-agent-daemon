// Package logging writes every log record twice — human-readable text to the
// console and one JSON object per line to the JSONL log — with secrets
// redacted from both.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Redacted replaces every secret in log output.
const Redacted = "[REDACTED]"

// tokenPattern matches GitHub token shapes, so a token is redacted even if it
// reaches the log before (or without) being registered with AddSecret.
var tokenPattern = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`)

// Logger is a slog.Logger whose sinks redact registered secrets.
type Logger struct {
	*slog.Logger
	secrets *secrets
}

// New returns a Logger writing text to console and JSONL to jsonl.
func New(console, jsonl io.Writer) *Logger {
	s := &secrets{}
	opts := &slog.HandlerOptions{ReplaceAttr: s.replaceAttr}
	h := slog.NewMultiHandler(
		slog.NewTextHandler(console, opts),
		slog.NewJSONHandler(jsonl, opts),
	)
	return &Logger{Logger: slog.New(h), secrets: s}
}

// OpenFile opens the JSONL log in the logs/ directory of the state
// directory, shared by the CLI and the tray app.
func OpenFile(stateDir string) (*os.File, error) {
	dir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "work-agent.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}

// AddSecret registers a value that must never appear in log output.
// Register secrets before deriving loggers with With: attributes bound by
// With are rendered at that moment.
func (l *Logger) AddSecret(secret string) {
	l.secrets.add(secret)
}

// Redact removes secrets from text that is printed outside the logger, such
// as an error message shown to the Operator.
func (l *Logger) Redact(text string) string {
	return l.secrets.redact(text)
}

type secrets struct {
	mu     sync.RWMutex
	values []string
}

func (s *secrets) add(v string) {
	if v == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = append(s.values, v)
}

func (s *secrets) redact(text string) string {
	s.mu.RLock()
	for _, v := range s.values {
		text = strings.ReplaceAll(text, v, Redacted)
	}
	s.mu.RUnlock()
	return tokenPattern.ReplaceAllString(text, Redacted)
}

func (s *secrets) replaceAttr(_ []string, a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(s.redact(v.String()))
	case slog.KindAny:
		switch x := v.Any().(type) {
		case []string:
			out := make([]string, len(x))
			for i, e := range x {
				out[i] = s.redact(e)
			}
			a.Value = slog.AnyValue(out)
		case error:
			a.Value = slog.StringValue(s.redact(x.Error()))
		default:
			text := fmt.Sprint(x)
			if r := s.redact(text); r != text {
				a.Value = slog.StringValue(r)
			}
		}
	}
	return a
}
