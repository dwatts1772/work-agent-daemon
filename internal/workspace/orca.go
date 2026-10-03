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
	repo, err := o.repo(ctx, in.Repo)
	if err != nil {
		return Workspace{}, err
	}
	return o.create(ctx, repo, "issue-"+strconv.Itoa(in.Issue), "--issue", strconv.Itoa(in.Issue))
}

// CreateForReview creates the Review Request's Review Workspace (ADR-0003):
// it fetches the PR's head from GitHub's pull ref (see FetchReviewHead),
// then creates a new Orca worktree on a new local branch review-pr-<n>
// starting at <remote>/pr/<n>. Orca creates that branch without tracking
// anything, so it has no path to push to the PR author's branch. Like
// CreateForIssue it starts no agent.
func (o *Orca) CreateForReview(ctx context.Context, in ReviewInput) (Workspace, error) {
	repo, base, err := o.fetchReviewHead(ctx, in)
	if err != nil {
		return Workspace{}, err
	}
	return o.create(ctx, repo, "review-pr-"+strconv.Itoa(in.PR), "--base-branch", base)
}

// FetchReviewHead fetches the PR's head from GitHub's pull ref, which works
// for forks too, into <remote>/pr/<n> in the repo's Orca clone, where a
// re-review in the existing Review Workspace finds it.
//
// It refuses when the fetched head is not in.HeadSHA, the head whose CI
// Settled: the author pushed since, and the new head is not yet Settled.
func (o *Orca) FetchReviewHead(ctx context.Context, in ReviewInput) error {
	_, _, err := o.fetchReviewHead(ctx, in)
	return err
}

// fetchReviewHead fetches the PR's head as FetchReviewHead does, returning
// the Orca repo and the ref it fetched into.
func (o *Orca) fetchReviewHead(ctx context.Context, in ReviewInput) (orcaRepo, string, error) {
	repo, err := o.repo(ctx, in.Repo)
	if err != nil {
		return orcaRepo{}, "", err
	}
	if repo.Path == "" || repo.Remote == "" {
		return orcaRepo{}, "", fmt.Errorf("orca repo list: %s has no local path or remote", in.Repo)
	}
	n := strconv.Itoa(in.PR)
	base := repo.Remote + "/pr/" + n
	if _, err := o.runner.Run(ctx, "git", []string{"-C", repo.Path, "fetch", repo.Remote, "+refs/pull/" + n + "/head:refs/remotes/" + base}); err != nil {
		return orcaRepo{}, "", fmt.Errorf("fetch the head of %s#%d: %w", in.Repo, in.PR, err)
	}
	out, err := o.runner.Run(ctx, "git", []string{"-C", repo.Path, "rev-parse", "refs/remotes/" + base})
	if err != nil {
		return orcaRepo{}, "", fmt.Errorf("read the fetched head of %s#%d: %w", in.Repo, in.PR, err)
	}
	if head := strings.TrimSpace(string(out)); head != in.HeadSHA {
		return orcaRepo{}, "", fmt.Errorf("the head of %s#%d moved from %s, whose CI Settled, to %s", in.Repo, in.PR, in.HeadSHA, head)
	}
	return repo, base, nil
}

// create creates a new Orca worktree called name in repo, with a fresh
// daemon-owned Claude session ID (ADR-0002).
func (o *Orca) create(ctx context.Context, repo orcaRepo, name string, extra ...string) (Workspace, error) {
	sessionID, err := newSessionID()
	if err != nil {
		return Workspace{}, err
	}
	var created struct {
		Worktree orcaWorktree `json:"worktree"`
	}
	args := append([]string{"worktree", "create",
		"--repo", "id:" + repo.ID,
		"--name", name,
		// The daemon is not working inside another Workspace; never let
		// Orca infer a parent from the caller's directory.
		"--no-parent",
	}, extra...)
	if err := o.call(ctx, &created, args...); err != nil {
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

// orcaRepo is a repo registered in Orca: its id, the path of its local
// clone, and the name of that clone's GitHub remote.
type orcaRepo struct {
	ID, Path, Remote string
}

// repo finds the Orca repo whose GitHub remote is repo ("owner/name").
func (o *Orca) repo(ctx context.Context, repo string) (orcaRepo, error) {
	var list struct {
		Repos []struct {
			ID                string `json:"id"`
			Path              string `json:"path"`
			GitRemoteIdentity *struct {
				CanonicalKey string `json:"canonicalKey"`
				RemoteName   string `json:"remoteName"`
			} `json:"gitRemoteIdentity"`
		} `json:"repos"`
	}
	if err := o.call(ctx, &list, "repo", "list"); err != nil {
		return orcaRepo{}, err
	}
	for _, r := range list.Repos {
		if r.GitRemoteIdentity != nil && strings.EqualFold(r.GitRemoteIdentity.CanonicalKey, "github.com/"+repo) {
			return orcaRepo{ID: r.ID, Path: r.Path, Remote: r.GitRemoteIdentity.RemoteName}, nil
		}
	}
	return orcaRepo{}, fmt.Errorf("%s is not registered in Orca; add a clone of it with `orca repo add --path <clone>`", repo)
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
//   - a live Claude whose agent is idle gets the prompt sent into it, and a
//     live Claude that is not idle makes the Wake fail with ErrAgentBusy;
//   - otherwise, if Claude has a transcript of the session, the
//     conversation is resumed with `claude --resume` in a new terminal;
//   - otherwise, as on the first Wake or when the session cannot be resumed,
//     a fresh session is started under the same ID with `claude --session-id`.
//
// Terminal handles are never kept; they are re-resolved on every Wake.
func (o *Orca) Wake(ctx context.Context, ws Workspace, prompt string) error {
	if !safePrompt.MatchString(prompt) {
		return fmt.Errorf("refusing to Wake with prompt %q: it contains characters a shell could interpret", prompt)
	}
	if !safeSessionID.MatchString(ws.ClaudeSessionID) {
		return fmt.Errorf("refusing to Wake: session ID %q is not a UUID", ws.ClaudeSessionID)
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
		if state != AgentIdle {
			return ErrAgentBusy
		}
		return o.call(ctx, nil, "terminal", "send", "--terminal", handle, "--text", prompt, "--enter")
	}
	if !o.hasTranscript(ws.ClaudeSessionID) {
		return o.startClaude(ctx, ws, fmt.Sprintf(`claude --session-id %s "%s"`, ws.ClaudeSessionID, prompt))
	}
	return o.startClaude(ctx, ws, fmt.Sprintf(`claude --resume %s "%s"`, ws.ClaudeSessionID, prompt))
}

// ErrAgentBusy is returned by Wake when Claude is live in the Workspace but
// not idle: typing into it or starting a second Claude beside it would Wake
// the Workspace twice concurrently, so the caller Holds the Wake.
var ErrAgentBusy = errors.New("Claude is live in the Workspace but not idle")

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
//
// This is how a Wake falls back to a fresh session "if resume fails"
// (ADR-0002): the Wake command is typed into the Workspace terminal's shell,
// whose exit status the daemon cannot observe, so a missing transcript —
// Claude never ran in the session, or cleaned it up — is taken to mean
// resume would fail.
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
