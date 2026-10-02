package process

import (
	"fmt"
	"strings"
)

// Check reports whether the runner may execute bin with args. The daemon is
// GitHub-read-mostly: gh is limited to read-only subcommands, git to a small
// set that excludes anything able to rewrite or delete remote history, and no
// other binary than gh, git, orca and claude can be run at all. This is what
// makes "no code path can merge or force push" true by construction.
func Check(bin string, args []string) error {
	switch bin {
	case "gh":
		return checkGH(args)
	case "git":
		return checkGit(args)
	case "orca", "claude":
		return nil
	default:
		return fmt.Errorf("binary %q is not allowlisted", bin)
	}
}

// ghAllowed lists the permitted gh commands as "command subcommand"; "api"
// stands alone because it is further restricted to GET requests.
var ghAllowed = map[string]bool{
	"auth token": true,
	"api":        true,
	"issue list": true,
	"issue view": true,
	"pr list":    true,
	"pr view":    true,
	"pr checks":  true,
}

func checkGH(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("gh: no command given")
	}
	cmd := args[0]
	if cmd != "api" && len(args) > 1 {
		cmd += " " + args[1]
	}
	if !ghAllowed[cmd] {
		return fmt.Errorf("gh %s is not allowlisted", cmd)
	}
	if cmd == "api" {
		return checkGHAPI(args[1:])
	}
	return nil
}

// checkGHAPI permits only GET requests. gh api switches to POST implicitly
// when fields or an input body are given, so those flags are rejected too.
func checkGHAPI(args []string) error {
	for i, a := range args {
		name, value, hasValue := strings.Cut(a, "=")
		switch {
		case name == "-X" || name == "--method":
			if !hasValue {
				if i+1 >= len(args) {
					return fmt.Errorf("gh api: %s needs a value", name)
				}
				value = args[i+1]
			}
			if !strings.EqualFold(value, "GET") {
				return fmt.Errorf("gh api: method %s is not allowed", value)
			}
		case strings.HasPrefix(a, "-X"):
			if !strings.EqualFold(strings.TrimPrefix(a, "-X"), "GET") {
				return fmt.Errorf("gh api: method %s is not allowed", strings.TrimPrefix(a, "-X"))
			}
		case name == "-f" || name == "-F" || name == "--field" || name == "--raw-field" || name == "--input",
			strings.HasPrefix(a, "-f") && !strings.HasPrefix(a, "--"),
			strings.HasPrefix(a, "-F"):
			return fmt.Errorf("gh api: %s would send a request body", name)
		}
	}
	return nil
}

var gitAllowed = map[string]bool{
	"fetch":     true,
	"rev-parse": true,
	"status":    true,
	"log":       true,
	"ls-remote": true,
	"show-ref":  true,
	"push":      true,
}

func checkGit(args []string) error {
	// Only -C <dir> may precede the subcommand; -c could define an alias
	// that runs arbitrary commands.
	for len(args) >= 2 && args[0] == "-C" {
		args = args[2:]
	}
	if len(args) == 0 {
		return fmt.Errorf("git: no subcommand given")
	}
	if !gitAllowed[args[0]] {
		return fmt.Errorf("git %s is not allowlisted", args[0])
	}
	if args[0] == "push" {
		return checkPush(args[1:])
	}
	return nil
}

// checkPush rejects every way git push can overwrite or delete a remote ref.
func checkPush(args []string) error {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		switch {
		case name == "--force", name == "--force-with-lease", name == "--force-if-includes",
			name == "--mirror", name == "--delete", name == "--prune":
			return fmt.Errorf("git push %s is not allowed", name)
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--"):
			if strings.ContainsAny(a[1:], "fd") {
				return fmt.Errorf("git push %s is not allowed", a)
			}
		case strings.HasPrefix(a, "+"):
			return fmt.Errorf("git push refspec %s would force-update", a)
		case strings.HasPrefix(a, ":"):
			return fmt.Errorf("git push refspec %s would delete a ref", a)
		}
	}
	return nil
}
