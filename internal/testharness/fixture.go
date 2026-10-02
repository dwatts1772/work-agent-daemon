// Package testharness provides stub gh, git, orca and claude executables on
// disk, so tests drive the real process runner without touching the network.
//
// All four stubs are copies of one program (./stub) that picks its behaviour
// from its own file name. Each copy reads fixture.json from its directory and
// appends every invocation to calls.jsonl there.
package testharness

const (
	FixtureFile = "fixture.json"
	CallsFile   = "calls.jsonl"
)

// Fixture is the GitHub world the stub gh simulates.
type Fixture struct {
	// Tokens maps an account to the token `gh auth token --user <account>`
	// prints. Accounts missing here are not logged in.
	Tokens map[string]string `json:"tokens"`
	// Users maps a token to the login `gh api user` reports for it.
	Users map[string]string `json:"users"`
	// ActiveAccount is gh's active account, used when no GH_TOKEN is set.
	// The daemon must never depend on it.
	ActiveAccount string `json:"activeAccount"`
	// Issues lists open issues per "owner/name" repo.
	Issues map[string][]Issue `json:"issues"`
	// EchoTokenOnError makes failing gh calls print GH_TOKEN to stderr, as
	// a hostile stand-in for any tool that leaks its credentials in errors.
	EchoTokenOnError bool `json:"echoTokenOnError"`
}

type Issue struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Labels    []string `json:"labels"`
	Assignees []string `json:"assignees"`
}

// Call is one recorded stub invocation.
type Call struct {
	Bin  string            `json:"bin"`
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
}

// RecordedEnv lists the environment variables a Call captures.
var RecordedEnv = []string{"GH_TOKEN", "GITHUB_TOKEN", "GIT_TERMINAL_PROMPT"}
