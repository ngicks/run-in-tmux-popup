package zellij

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/shellargv"
)

// RunRequest is a "zellij run" invocation. zellij addresses sessions, not
// clients, so there is no client targeting.
type RunRequest struct {
	// SessionId is the session hosting the pane (--session). Empty lets zellij
	// resolve the current one.
	SessionId string
	// Title names the pane (--name). Empty leaves zellij's default.
	Title string
	// EnvFile is a shell file the payload sources before it runs, holding the
	// pane's environment. zellij has no environment flag of its own, and an argv
	// is readable by every process for as long as the pane lives, so the values
	// travel in a file — WriteEnvFile writes one — and only its path is on the
	// command line.
	EnvFile string
	// X, Y, Width and Height place and size the floating pane (--x, --y, --width,
	// --height). zellij takes a bare number of cells or a percentage, and nothing
	// else: the single-letter positions tmux understands have no equivalent here
	// and are refused by whoever builds the request. Empty leaves zellij's
	// default.
	//
	// X and Y are the pane's top-left corner, which is what zellij's own flags
	// take, so they travel as they were written.
	X, Y, Width, Height string
	// Command is the argv the pane runs.
	Command []string
	// Script is a raw shell command line taking precedence over Command.
	Script string
}

// RunCommand builds "zellij --session=<id> run [--name=<title>] [--x=<x>]
// [--y=<y>] [--width=<width>] [--height=<height>] --floating --close-on-exit
// --pinned=true -- <payload>".
func (c *Client) RunCommand(req RunRequest) (path string, args []string) {
	if req.SessionId != "" {
		args = append(args, "--session="+req.SessionId)
	}
	args = append(args, "run")
	if req.Title != "" {
		args = append(args, "--name="+req.Title)
	}
	args = append(args, geometryArgs(req)...)
	args = append(args, "--floating", "--close-on-exit", "--pinned=true", "--")
	return c.path, append(args, c.payload(req)...)
}

// StartRun runs RunCommand's argv. "zellij run" returns as soon as the floating
// pane exists, so its launcher says nothing about the payload still running in
// it.
func (c *Client) StartRun(ctx context.Context, req RunRequest) (*Launcher, error) {
	_, args := c.RunCommand(req)
	l, err := c.start(ctx, args)
	if err != nil {
		return nil, err
	}
	l.close = func(ctx context.Context) error { return c.closePane(ctx, req.SessionId, l) }
	return l, nil
}

// closePane closes the floating pane the launcher created. The id it is named by
// is on the launcher's own stdout, so the launcher is waited on first — reading
// that buffer beside a zellij still writing it is the race this waits out, not
// merely a stale read.
func (c *Client) closePane(ctx context.Context, sessionId string, l *Launcher) error {
	if err := l.waitBounded(ctx); err != nil {
		return fmt.Errorf("%s: waiting for the new pane's id: %w", l.line, err)
	}
	paneId, ok := parsePaneId(l.stdout.String())
	if !ok {
		return fmt.Errorf(
			"%s: no pane id to close, the launcher printed %q",
			l.line, strings.TrimSpace(l.stdout.String()+l.stderr.String()),
		)
	}
	return c.ClosePane(ctx, sessionId, paneId)
}

// paneIdPrefix is what "zellij run" calls the pane it creates. Its --help says
// it prints the created pane's id as "terminal_<id>", and that is the form
// "zellij action close-pane --pane-id" takes back.
const paneIdPrefix = "terminal_"

// parsePaneId picks the pane id out of what "zellij run" printed: the first
// word shaped like "terminal_<n>", so a sentence around it changes nothing and
// output without one is a launcher that never got as far as creating a pane.
func parsePaneId(out string) (string, bool) {
	for field := range strings.FieldsSeq(out) {
		digits, ok := strings.CutPrefix(field, paneIdPrefix)
		if !ok || digits == "" {
			continue
		}
		if strings.ContainsFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) {
			continue
		}
		return field, true
	}
	return "", false
}

// ClosePaneCommand builds "zellij [--session=<id>] action close-pane
// --pane-id=<pane>". The session travels as the flag every other command here
// takes, since a pane id names a pane within a session and nothing beyond it.
func (c *Client) ClosePaneCommand(sessionId, paneId string) (path string, args []string) {
	if sessionId != "" {
		args = append(args, "--session="+sessionId)
	}
	return c.path, append(args, "action", "close-pane", "--pane-id="+paneId)
}

// ClosePane closes the pane a matching StartRun created, taking whatever runs in
// it along.
func (c *Client) ClosePane(ctx context.Context, sessionId, paneId string) error {
	_, args := c.ClosePaneCommand(sessionId, paneId)
	return c.run(ctx, args...)
}

// payload renders what the pane executes. zellij runs an argv directly, so a
// script payload — or an environment, which only a shell can read in — is
// wrapped in a shell.
func (c *Client) payload(req RunRequest) []string {
	if req.Script == "" && req.EnvFile == "" {
		return slices.Clone(req.Command)
	}
	line := commandLine(req.Command, req.Script)
	if req.EnvFile != "" {
		// A pane that could not read its environment must not run the payload
		// without it, hence "&&" rather than the ";" an inline export would take;
		// the payload is grouped so that the gate covers all of it and not just
		// the first command of a Script. The newline is what makes the closing
		// brace a command of its own, whatever the payload ends with.
		line = fmt.Sprintf(". %s && { %s\n}", shellargv.Quote(req.EnvFile), line)
	}
	return []string{c.shell, "-c", line}
}

// envFileName is what WriteEnvFile calls its file. The work directory it lands
// in also holds the launch's payload FIFOs and, during a pinentry exchange, the
// handshake ones, so the name stays clear of all of theirs.
const envFileName = "env"

// WriteEnvFile writes env as a shell file in dir and returns its path, for
// RunRequest.EnvFile. It is the counterpart of the sourcing payload renders:
// where a multiplexer has no environment flag, this is how the values reach the
// pane without passing through an argv.
//
// The file is mode 0600 — it exists to keep the values off a command line every
// process can read, and would be no improvement if it were readable too.
func WriteEnvFile(dir string, env map[string]string) (string, error) {
	var sb strings.Builder
	for _, k := range slices.Sorted(maps.Keys(env)) {
		fmt.Fprintf(&sb, "export %s=%s\n", k, shellargv.Quote(env[k]))
	}
	path := filepath.Join(dir, envFileName)
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		return "", fmt.Errorf("writing the popup environment: %w", err)
	}
	return path, nil
}

// geometryArgs renders a pane's placement and size as "zellij run" flags, in
// the order --x, --y, --width, --height. An empty value emits nothing at all,
// leaving zellij's own placement.
func geometryArgs(req RunRequest) []string {
	var args []string
	for _, f := range []struct{ flag, value string }{
		{"--x", req.X},
		{"--y", req.Y},
		{"--width", req.Width},
		{"--height", req.Height},
	} {
		if f.value != "" {
			args = append(args, f.flag+"="+f.value)
		}
	}
	return args
}

// commandLine renders a payload as a single shell command line.
func commandLine(command []string, script string) string {
	if script != "" {
		return script
	}
	return shellargv.Join(command)
}
