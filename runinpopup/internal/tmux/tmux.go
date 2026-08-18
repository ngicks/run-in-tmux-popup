// Package tmux speaks to the tmux executable: it builds every argv this module
// sends to it, carries the environment those commands need, and parses what
// they print back. Callers decide which tmux operation expresses their popup;
// how tmux is asked for it lives here.
package tmux

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// Options are the coordinates of the tmux server a Client talks to.
type Options struct {
	// Path is the tmux executable. Empty means "tmux".
	Path string
	// SessionMeta is a $TMUX value naming the server hosting the popup, for
	// callers that are not inside tmux themselves.
	SessionMeta string
	// TMUX is the caller's current $TMUX value.
	TMUX string
}

// Client runs tmux commands against one server.
type Client struct {
	path string
	env  []string
}

// New builds a client from the server coordinates.
//
// SessionMeta is only validated when it is the value that will be used, i.e.
// when TMUX is empty: a caller already inside tmux does not need it at all.
func New(opts Options) (*Client, error) {
	if err := validateSessionMeta(opts.SessionMeta, opts.TMUX); err != nil {
		return nil, err
	}
	return &Client{
		path: cmp.Or(opts.Path, "tmux"),
		env:  sessionEnviron(opts.SessionMeta, opts.TMUX),
	}, nil
}

// Path reports the tmux executable the client runs.
func (c *Client) Path() string { return c.path }

// Environ returns the "KEY=VALUE" adjustments a tmux command needs on top of
// the current process environment, or nil when it needs none.
func (c *Client) Environ() []string { return slices.Clone(c.env) }

// validateSessionMeta rejects a session meta that could not stand in for $TMUX.
// It only matters when it is the value that will be used, i.e. when tmuxEnv is
// empty.
func validateSessionMeta(sessionMeta, tmuxEnv string) error {
	if tmuxEnv != "" || strings.Contains(sessionMeta, ",") {
		return nil
	}
	return fmt.Errorf(
		"tmux session meta is malformed:"+
			" it must be something like %q but is %q",
		"/run/user/1000/tmux-1000/default,111,0", sessionMeta,
	)
}

// sessionEnviron sets $TMUX from the session meta when the current process has
// none. Without it every tmux command addresses the default socket instead of
// the server hosting the popup: the popup silently never appears.
func sessionEnviron(sessionMeta, tmuxEnv string) []string {
	if tmuxEnv != "" || sessionMeta == "" {
		return nil
	}
	return []string{"TMUX=" + sessionMeta}
}

// targeted appends "-t <session>" to a tmux command, so a query inspects the
// window the popup will open in. Without a session id tmux resolves the current
// session for both, so they stay consistent — just less explicit.
func targeted(sessionId string, args ...string) []string {
	if sessionId == "" {
		return args
	}
	return append(args, "-t", sessionId)
}

// run executes a tmux command carrying the same environment the popup command
// gets — without it a query would inspect the default socket's server while the
// popup opens on another — and folds the command's stderr into the error, where
// tmux reports "can't find pane" and friends.
func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.path, args...)
	if len(c.env) > 0 {
		cmd.Env = append(os.Environ(), c.env...)
	}
	out, err := cmd.Output()
	if execErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return "", fmt.Errorf(
			"%s %s: %w: %s",
			c.path, strings.Join(args, " "), err, strings.TrimSpace(string(execErr.Stderr)),
		)
	}
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", c.path, strings.Join(args, " "), err)
	}
	return string(out), nil
}
