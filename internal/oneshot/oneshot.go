// Package oneshot parses "tailsplit.exe app.exe --exit-node name [-- args]".
package oneshot

import (
	"errors"
	"strings"
)

type Args struct {
	App      string   // as typed: a path or a bare name
	ExitNode string   // empty: use the saved exit node
	AppArgs  []string // passed to the app, after "--"
}

// LooksLikeApp tells a one-shot invocation from a subcommand.
func LooksLikeApp(first string) bool {
	return strings.HasSuffix(strings.ToLower(first), ".exe") || strings.ContainsAny(first, `\/`)
}

// Parse accepts the flags before or after the app, as --exit-node, --exitnode
// or -e, with the value separated by a space or "=".
func Parse(args []string) (Args, error) {
	var a Args
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			a.AppArgs = append([]string(nil), args[i+1:]...)
			break
		}
		name, val, hasVal := strings.Cut(arg, "=")
		switch strings.ToLower(name) {
		case "--exit-node", "--exitnode", "-exit-node", "-exitnode", "-e":
			if !hasVal {
				if i+1 >= len(args) {
					return a, errors.New(name + " needs a value")
				}
				i++
				val = args[i]
			}
			a.ExitNode = val
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return a, errors.New("unknown option " + arg + " (app arguments go after --)")
		}
		if a.App != "" {
			return a, errors.New("only one app per run; app arguments go after --")
		}
		a.App = arg
	}
	if a.App == "" {
		return a, errors.New("no app given")
	}
	return a, nil
}
