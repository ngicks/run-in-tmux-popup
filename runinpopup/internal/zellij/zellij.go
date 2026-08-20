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
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
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

// start runs a zellij command in the background. Canceling ctx interrupts it,
// which tears the launcher down; closing the pane it opened is Dismiss.
func (c *Client) start(ctx context.Context, args []string) (*Launcher, error) {
	l := &Launcher{line: c.path + " " + strings.Join(args, " ")}
	cmd := exec.CommandContext(ctx, c.path, args...)
	// SIGINT rather than the default kill: zellij gets to tear its client down
	// itself instead of being cut off mid-write.
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
	l.wait = sync.OnceValue(l.reap)
	return l, nil
}

// run executes a zellij command and folds its stderr into the error, where
// zellij reports the session and the pane it could not find.
func (c *Client) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, c.path, args...)
	_, err := cmd.Output()
	if execErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return fmt.Errorf(
			"%s %s: %w: %s",
			c.path, strings.Join(args, " "), err, strings.TrimSpace(string(execErr.Stderr)),
		)
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", c.path, strings.Join(args, " "), err)
	}
	return nil
}

// launcherWaitDelay is how long a dismissed launcher has to go away on its own
// before it is killed. A zellij client that ignores its interrupt, or that
// leaves a child holding the pipes its output is read from, would otherwise be
// waited on forever.
const launcherWaitDelay = 2 * time.Second

// Launcher is a running zellij command that opens a floating pane, and the way
// to close the pane it opened. Its own streams are of no interest to the
// payload, which draws on the pane rather than on this process's terminal.
type Launcher struct {
	cmd  *exec.Cmd
	line string
	// The launcher's own output is diagnostics, reported only with a failure —
	// and the one place the created pane's id is. The two streams are kept apart
	// while zellij writes them and joined only in that error, where which
	// descriptor carried a message says nothing.
	stdout, stderr bytes.Buffer
	// wait is memoized: a process can only be reaped once, while both the caller
	// waiting on the launcher and a dismissal reading what it printed have to go
	// through it.
	wait func() error
	// close closes the pane this launcher opened, set by the Start that built it.
	close     func(context.Context) error
	closeOnce sync.Once
	closeErr  error
}

// Wait waits for the launcher to exit and decorates a failure with the command
// that produced it and everything it printed, which is the only trace a pane
// that never appeared leaves.
func (l *Launcher) Wait() error { return l.wait() }

func (l *Launcher) reap() error {
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

// Dismiss closes the pane this launcher opened, once per launcher: the close
// command is the pane's end, and a second one could only be told the pane is
// already gone.
func (l *Launcher) Dismiss(ctx context.Context) error {
	l.closeOnce.Do(func() { l.closeErr = l.close(ctx) })
	return l.closeErr
}

// waitBounded waits for the launcher to exit, giving up when ctx does. Only the
// bound is reported: how the launcher itself exited says nothing about whether
// the pane it created is still there — an interrupted one may well have printed
// the id of a pane that outlives it.
//
// The goroutine outlives a bound that ran out, and has to: a wait cannot be
// taken back, and this one ends when the launcher does.
func (l *Launcher) waitBounded(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = l.Wait()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
