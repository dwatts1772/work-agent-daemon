package process

import (
	"fmt"
	"strings"
)

// Check reports whether the runner may execute bin with args. The daemon is
// GitHub-read-only: gh is limited to the read commands the daemon uses, git
// to commands that cannot change any remote (there is no push at all), and
// no other binary than gh, git, orca and claude can be run. This is what
// makes "no code path can merge or force push" true by construction.
//
// Grow the allowlists only when a feature needs a command, and test the
// flags that could turn it into a write.
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

// ghAPIValueShorts are gh api's short flags that take a value; in a bundle
// such as -iXPUT the rest of the bundle is that value.
const ghAPIValueShorts = "XFfHpqt"

// checkGHAPI permits only GET requests to github.com. gh api switches to POST
// implicitly when fields or an input body are given, so those flags are
// rejected too. gh (pflag) bundles short flags and does not accept
// abbreviated long flags.
func checkGHAPI(args []string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--"):
			name, value, hasValue := strings.Cut(a, "=")
			switch name {
			case "--method":
				if !hasValue {
					i++
					if i >= len(args) {
						return fmt.Errorf("gh api: --method needs a value")
					}
					value = args[i]
				}
				if !strings.EqualFold(value, "GET") {
					return fmt.Errorf("gh api: method %s is not allowed", value)
				}
			case "--field", "--raw-field", "--input":
				return fmt.Errorf("gh api: %s would send a request body", name)
			case "--hostname":
				return fmt.Errorf("gh api: --hostname is not allowed")
			case "--header", "--jq", "--template", "--preview", "--cache":
				if !hasValue {
					i++
				}
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			consumedNext, err := checkGHAPIShorts(a[1:], args[i+1:])
			if err != nil {
				return err
			}
			if consumedNext {
				i++
			}
		}
	}
	return nil
}

// checkGHAPIShorts checks one bundle of short flags, reporting whether its
// last flag took the following argument as its value.
func checkGHAPIShorts(bundle string, rest []string) (consumedNext bool, err error) {
	for j, c := range bundle {
		if !strings.ContainsRune(ghAPIValueShorts, c) {
			continue
		}
		value := bundle[j+1:]
		if value == "" {
			if len(rest) == 0 {
				return false, fmt.Errorf("gh api: -%c needs a value", c)
			}
			value, consumedNext = rest[0], true
		}
		switch c {
		case 'X':
			if !strings.EqualFold(value, "GET") {
				return false, fmt.Errorf("gh api: method %s is not allowed", value)
			}
		case 'f', 'F':
			return false, fmt.Errorf("gh api: -%c would send a request body", c)
		}
		return consumedNext, nil
	}
	return false, nil
}

var gitAllowed = map[string]bool{
	"fetch":     true,
	"rev-parse": true,
	"status":    true,
}

// gitExecOptions make git run a command of the caller's choosing.
var gitExecOptions = []string{"--upload-pack", "--receive-pack", "--exec"}

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
	for _, a := range args[1:] {
		name, _, _ := strings.Cut(a, "=")
		// git accepts any unambiguous prefix of a long option.
		if len(name) <= 2 || !strings.HasPrefix(name, "--") {
			continue
		}
		for _, opt := range gitExecOptions {
			if strings.HasPrefix(opt, name) {
				return fmt.Errorf("git %s %s is not allowed", args[0], name)
			}
		}
	}
	return nil
}
