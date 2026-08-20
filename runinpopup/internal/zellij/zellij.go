// Package zellij speaks to the zellij executable: it builds every argv this
// module sends to it, including the shell wrapping zellij needs for payloads it
// cannot run as a bare argv, and the environment file such a payload sources.
// Callers decide which zellij operation expresses their popup; how zellij is
// asked for it lives here.
package zellij

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

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

	l := &Launcher{line: c.path + " " + strings.Join(args, " ")}
	cmd := exec.CommandContext(ctx, c.path, args...)
	// SIGINT rather than the default kill: it is what dismisses a pane, and
	// zellij gets to tear its client down itself.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGINT)
	}
	cmd.WaitDelay = launcherWaitDelay
	cmd.Stdout = &l.stdout
	cmd.Stderr = &l.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", l.line, err)
	}
	l.cmd = cmd
	return l, nil
}

// launcherWaitDelay is how long a dismissed launcher has to go away on its own
// before it is killed. A zellij client that ignores its interrupt, or that
// leaves a child holding the pipes its output is read from, would otherwise be
// waited on forever.
const launcherWaitDelay = 2 * time.Second

// Launcher is a running zellij command that opens a floating pane. Only its exit
// is of interest: the payload draws on the pane, not on this process's terminal.
type Launcher struct {
	cmd  *exec.Cmd
	line string
	// The launcher's own output is diagnostics, reported only with a failure.
	// The two streams are kept apart while zellij writes them and joined only in
	// that error, where which descriptor carried a message says nothing.
	stdout, stderr bytes.Buffer
}

// Wait waits for the launcher to exit and decorates a failure with the command
// that produced it and everything it printed, which is the only trace a pane
// that never appeared leaves.
func (l *Launcher) Wait() error {
	err := l.cmd.Wait()
	if err == nil {
		return nil
	}
	err = fmt.Errorf("%s: %w", l.line, err)
	if out := strings.TrimSpace(l.stdout.String() + l.stderr.String()); out != "" {
		err = fmt.Errorf("%w: %s", err, out)
	}
	return err
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
