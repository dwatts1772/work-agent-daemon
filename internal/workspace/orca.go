package workspace

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Runner runs an allowlisted binary with an argument array; process.Runner
// implements it.
type Runner interface {
	Run(ctx context.Context, bin string, args []string, env ...string) ([]byte, error)
}

// Orca is the Backend backed by the Orca CLI (verified against Orca 1.4).
// Every call is `orca … --json`; it never launches Orca.
type Orca struct {
	runner Runner
	// claudeDir is Claude's config directory, where it keeps the
	// transcripts of the sessions it can resume.
	claudeDir string
}

var _ Backend = (*Orca)(nil)

// NewOrca returns the Orca adapter running orca through runner, for a
// Claude whose config directory is claudeDir.
func NewOrca(runner Runner, claudeDir string) *Orca {
	return &Orca{runner: runner, claudeDir: claudeDir}
}

// ClaudeDir is Claude's config directory: $CLAUDE_CONFIG_DIR, else
// ~/.claude.
func ClaudeDir() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// OrcaError is a failure Orca reported in its JSON envelope.
type OrcaError struct {
	Command string
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *OrcaError) Error() string {
	return fmt.Sprintf("orca %s: %s: %s", e.Command, e.Code, e.Message)
}

// call runs `orca <args> --json` and decodes the envelope's result into out.
// Orca exits non-zero on failure but still prints the envelope, so a
// readable error from Orca takes precedence over the exit status.
func (o *Orca) call(ctx context.Context, out any, args ...string) error {
	stdout, runErr := o.runner.Run(ctx, "orca", append(args, "--json"))
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *OrcaError      `json:"error"`
	}
	if err := json.Unmarshal(stdout, &env); err != nil {
		if runErr != nil {
			return runErr
		}
		return fmt.Errorf("orca %s: unreadable response: %w", strings.Join(args, " "), err)
	}
	if !env.OK {
		e := env.Error
		if e == nil {
			e = &OrcaError{Code: "unknown", Message: "ok is false"}
		}
		e.Command = strings.Join(args[:min(2, len(args))], " ")
		return e
	}
	if runErr != nil {
		return runErr
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// Available reports whether an Orca runtime is running and ready. A failure
// to ask is reported as unavailable, with the reason.
func (o *Orca) Available(ctx context.Context) (bool, error) {
	var status struct {
		Runtime struct {
			State     string `json:"state"`
			Reachable bool   `json:"reachable"`
		} `json:"runtime"`
	}
	if err := o.call(ctx, &status, "status"); err != nil {
		return false, err
	}
	return status.Runtime.Reachable && status.Runtime.State == "ready", nil
}

// CreateForIssue creates the Owned Issue's Workspace as a new Orca worktree
// linked to the issue, with a fresh daemon-owned Claude session ID
// (ADR-0002). It starts no agent: the first Wake starts Claude with that ID.
func (o *Orca) CreateForIssue(ctx context.Context, in CreateInput) (Workspace, error) {
	repoID, err := o.repoID(ctx, in.Repo)
	if err != nil {
		return Workspace{}, err
	}
	sessionID, err := newSessionID()
	if err != nil {
		return Workspace{}, err
	}
	name := "issue-" + strconv.Itoa(in.Issue)
	var created struct {
		Worktree orcaWorktree `json:"worktree"`
	}
	err = o.call(ctx, &created, "worktree", "create",
		"--repo", "id:"+repoID,
		"--name", name,
		"--issue", strconv.Itoa(in.Issue),
		// The daemon is not working inside another Workspace; never let
		// Orca infer a parent from the caller's directory.
		"--no-parent",
	)
	if err != nil {
		return Workspace{}, err
	}
	wt := created.Worktree
	if wt.Identity.Key == "" || wt.Path == "" {
		return Workspace{}, fmt.Errorf("orca worktree create: response has no identity key or path")
	}
	return Workspace{
		OrcaIdentityKey: wt.Identity.Key,
		Path:            wt.Path,
		Branch:          strings.TrimPrefix(wt.Branch, "refs/heads/"),
		ClaudeSessionID: sessionID,
	}, nil
}

type orcaWorktree struct {
	Identity struct {
		Key string `json:"key"`
	} `json:"identity"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

// repoID finds the Orca repo whose GitHub remote is repo ("owner/name").
func (o *Orca) repoID(ctx context.Context, repo string) (string, error) {
	var list struct {
		Repos []struct {
			ID                string `json:"id"`
			GitRemoteIdentity *struct {
				CanonicalKey string `json:"canonicalKey"`
			} `json:"gitRemoteIdentity"`
		} `json:"repos"`
	}
	if err := o.call(ctx, &list, "repo", "list"); err != nil {
		return "", err
	}
	for _, r := range list.Repos {
		if r.GitRemoteIdentity != nil && strings.EqualFold(r.GitRemoteIdentity.CanonicalKey, "github.com/"+repo) {
			return r.ID, nil
		}
	}
	return "", fmt.Errorf("%s is not registered in Orca; add a clone of it with `orca repo add --path <clone>`", repo)
}

// newSessionID returns a random (version 4) UUID.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// psLimit is far above the number of worktrees one Operator has.
const psLimit = "1000"

// AgentState reads the live state of ws's agents from `worktree ps`, which
// Orca derives from its Claude hooks. Any working agent makes the Workspace
// working; otherwise any agent waiting on the Operator (or blocked) makes it
// waiting.
func (o *Orca) AgentState(ctx context.Context, ws Workspace) (AgentState, error) {
	var ps struct {
		Worktrees []struct {
			Path   string `json:"path"`
			Agents []struct {
				State string `json:"state"`
			} `json:"agents"`
		} `json:"worktrees"`
	}
	if err := o.call(ctx, &ps, "worktree", "ps", "--limit", psLimit); err != nil {
		return AgentNone, err
	}
	state := AgentNone
	for _, wt := range ps.Worktrees {
		if !samePath(wt.Path, ws.Path) {
			continue
		}
		for _, a := range wt.Agents {
			switch a.State {
			case "working":
				return AgentWorking, nil
			case "waiting", "blocked":
				state = AgentWaiting
			default:
				if state == AgentNone {
					state = AgentIdle
				}
			}
		}
	}
	return state, nil
}

// samePath compares Orca paths, which use forward slashes on every OS, and
// treats case as insignificant (Windows and default macOS file systems).
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.ToSlash(a), filepath.ToSlash(b))
}

// safePrompt is what a Wake prompt may contain. Orca types --command into the
// Workspace terminal's shell (PowerShell, cmd, bash or zsh), so anything a
// shell could interpret inside double quotes is refused rather than escaped.
var (
	safePrompt    = regexp.MustCompile(`^[A-Za-z0-9/._:#@+-]+( [A-Za-z0-9/._:#@+-]+)*$`)
	safeSessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// Wake hands the Workspace to Claude with prompt, keeping one conversation
// per Workspace under the daemon-owned session ID (ADR-0002):
//
//   - a live Claude whose agent is idle gets the prompt sent into it;
//   - otherwise, if Claude has a transcript of the session, the
//     conversation is resumed with `claude --resume` in a new terminal;
//   - otherwise, as on the first Wake or when the session cannot be resumed,
//     a fresh session is started under the same ID with `claude --session-id`.
//
// Terminal handles are never kept; they are re-resolved on every Wake. The
// caller holds the Wake while the agent is working.
func (o *Orca) Wake(ctx context.Context, ws Workspace, prompt string) error {
	if !safePrompt.MatchString(prompt) {
		return fmt.Errorf("refusing to Wake with prompt %q: it contains characters a shell could interpret", prompt)
	}
	if !safeSessionID.MatchString(ws.ClaudeSessionID) {
		return fmt.Errorf("refusing to Wake: session ID %q is not a UUID", ws.ClaudeSessionID)
	}
	if !o.hasTranscript(ws.ClaudeSessionID) {
		return o.startClaude(ctx, ws, fmt.Sprintf(`claude --session-id %s "%s"`, ws.ClaudeSessionID, prompt))
	}
	handle, err := o.liveClaude(ctx, ws)
	if err != nil {
		return err
	}
	if handle != "" {
		state, err := o.AgentState(ctx, ws)
		if err != nil {
			return err
		}
		if state == AgentIdle {
			return o.call(ctx, nil, "terminal", "send", "--terminal", handle, "--text", prompt, "--enter")
		}
	}
	return o.startClaude(ctx, ws, fmt.Sprintf(`claude --resume %s "%s"`, ws.ClaudeSessionID, prompt))
}

// startClaude runs command in a new terminal of ws.
func (o *Orca) startClaude(ctx context.Context, ws Workspace, command string) error {
	return o.call(ctx, nil, "terminal", "create",
		"--worktree", "identity:"+ws.OrcaIdentityKey,
		"--command", command,
	)
}

// liveClaude returns the handle of a live, writable terminal in ws running
// Claude, or "" if there is none.
func (o *Orca) liveClaude(ctx context.Context, ws Workspace) (string, error) {
	var list struct {
		Terminals []struct {
			Handle        string `json:"handle"`
			AgentIdentity string `json:"agentIdentity"`
			Connected     bool   `json:"connected"`
			Writable      bool   `json:"writable"`
			Orphaned      bool   `json:"orphaned"`
		} `json:"terminals"`
	}
	if err := o.call(ctx, &list, "terminal", "list", "--worktree", "identity:"+ws.OrcaIdentityKey); err != nil {
		return "", err
	}
	for _, t := range list.Terminals {
		if t.AgentIdentity == "claude" && t.Connected && t.Writable && !t.Orphaned && t.Handle != "" {
			return t.Handle, nil
		}
	}
	return "", nil
}

// hasTranscript reports whether Claude keeps a transcript of session, which
// `claude --resume` needs. Claude stores it as projects/<project>/<id>.jsonl
// in its config directory; session IDs are UUIDs, so any project will do.
func (o *Orca) hasTranscript(session string) bool {
	matches, _ := filepath.Glob(filepath.Join(o.claudeDir, "projects", "*", session+".jsonl"))
	return len(matches) > 0
}

// Exists reports whether Orca still has ws's worktree. It is an error, not
// false, when Orca cannot be asked.
func (o *Orca) Exists(ctx context.Context, ws Workspace) (bool, error) {
	err := o.call(ctx, nil, "worktree", "show", "--worktree", "identity:"+ws.OrcaIdentityKey)
	var orcaErr *OrcaError
	if errors.As(err, &orcaErr) && orcaErr.Code == "selector_not_found" {
		return false, nil
	}
	return err == nil, err
}
