// Package zellij speaks to the zellij executable: it builds every argv this
// module sends to it, including the shell wrapping zellij needs for payloads it
// cannot run as a bare argv. Callers decide which zellij operation expresses
// their popup; how zellij is asked for it lives here.
package zellij

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/shellargv"
)

// Options are the coordinates of the zellij installation a Client talks to.
type Options struct {
	// Path is the zellij executable. Empty means "zellij".
	Path string
	// Shell runs the payloads zellij cannot run as a bare argv. Empty means
	// "sh".
	Shell string
}

// Client runs zellij commands.
type Client struct {
	path  string
	shell string
}

// New builds a client.
func New(opts Options) *Client {
	return &Client{
		path:  cmp.Or(opts.Path, "zellij"),
		shell: cmp.Or(opts.Shell, "sh"),
	}
}

// RunRequest is a "zellij run" invocation. zellij addresses sessions, not
// clients, so there is no client targeting.
type RunRequest struct {
	// SessionId is the session hosting the pane (--session). Empty lets zellij
	// resolve the current one.
	SessionId string
	// Title names the pane (--name). Empty leaves zellij's default.
	Title string
	// Env is exported by the shell the payload is wrapped in: zellij has no
	// environment flag of its own.
	Env map[string]string
	// Command is the argv the pane runs.
	Command []string
	// Script is a raw shell command line taking precedence over Command.
	Script string
}

// RunCommand builds "zellij --session=<id> run [--name=<title>] --floating
// --close-on-exit --pinned=true -- <payload>".
func (c *Client) RunCommand(req RunRequest) (path string, args []string) {
	if req.SessionId != "" {
		args = append(args, "--session="+req.SessionId)
	}
	args = append(args, "run")
	if req.Title != "" {
		args = append(args, "--name="+req.Title)
	}
	args = append(args, "--floating", "--close-on-exit", "--pinned=true", "--")
	return c.path, append(args, c.payload(req)...)
}

// payload renders what the pane executes. zellij runs an argv directly, so a
// script payload — or an env injection, for which zellij has no flag — is
// wrapped in a shell.
func (c *Client) payload(req RunRequest) []string {
	if req.Script == "" && len(req.Env) == 0 {
		return slices.Clone(req.Command)
	}
	var sb strings.Builder
	for _, k := range slices.Sorted(maps.Keys(req.Env)) {
		fmt.Fprintf(&sb, "export %s=%s; ", k, shellargv.Quote(req.Env[k]))
	}
	sb.WriteString(commandLine(req.Command, req.Script))
	return []string{c.shell, "-c", sb.String()}
}

// commandLine renders a payload as a single shell command line.
func commandLine(command []string, script string) string {
	if script != "" {
		return script
	}
	return shellargv.Join(command)
}
