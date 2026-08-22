package runinpopup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
)

// PinentryLauncher proxies the Assuan exchange gpg-agent runs over this
// process's stdin and stdout to a pinentry drawing in a popup.
//
// It opens a popup whose only job is to report the terminal it runs on and stay
// alive, runs pinentry outside the popup on that terminal, and rewrites the
// "OPTION ttyname=" line gpg-agent sends so the prompt appears there instead of
// on whichever terminal gpg-agent happened to pick. The popup is dismissed once
// pinentry exits.
//
// A launcher is one-shot in the exec.Cmd sense: fill the fields in, call Call
// once.
type PinentryLauncher struct {
	// Popup opens the popup and supplies everything the exchange runs on: the
	// backend, the workspace holding the handshake FIFOs and the bound on the
	// rendezvous with the payload. Required, and its Backend must implement
	// TTYHandshaker.
	Popup *PopupLauncher
	// PinentryPath is the pinentry binary run outside the popup, on the terminal
	// the popup reported. Empty means DefaultConfig().PinentryPath.
	PinentryPath string
	// PinentryArgs is passed through to that binary, normally this process's own
	// arguments as gpg-agent invoked them.
	PinentryArgs []string
	// Timeouts bounds each stage of the exchange. Zero fields fall back to
	// DefaultConfig().Timeouts.
	Timeouts TimeoutsConfig

	// The process stdio the exchange runs on, nil meaning os.Stdin, os.Stdout and
	// os.Stderr: the input is relayed through a pipe the exchange may close, the
	// two outputs are handed to the pinentry child as they are. Unexported because
	// a caller has nothing to gain from redirecting them — gpg-agent speaks Assuan
	// over this process's own stdio and nowhere else; they exist so tests can drive
	// the exchange without handing it the test runner's own streams.
	stdin          io.Reader
	stdout, stderr io.Writer
}

// Call runs the exchange: it returns once pinentry has exited and the popup has
// been told to go away, or once the whole exchange has run past
// Timeouts.Overall.
func (l *PinentryLauncher) Call(ctx context.Context) (err error) {
	if l.Popup == nil {
		return errors.New("PinentryLauncher.Popup must be set")
	}
	if l.Popup.Backend == nil {
		return errors.New("PinentryLauncher.Popup.Backend must be set")
	}
	handshaker, ok := l.Popup.Backend.(TTYHandshaker)
	if !ok {
		return fmt.Errorf(
			"backend %q does not implement runinpopup.TTYHandshaker:"+
				" it cannot host the pinentry tty handshake",
			l.Popup.Backend.Name(),
		)
	}

	def := DefaultConfig()
	timeouts := TimeoutsConfig{
		Overall:   cmp.Or(l.Timeouts.Overall, def.Timeouts.Overall),
		TTYRead:   cmp.Or(l.Timeouts.TTYRead, def.Timeouts.TTYRead),
		DoneWrite: cmp.Or(l.Timeouts.DoneWrite, def.Timeouts.DoneWrite),
	}
	logger := loggerOrDiscard(l.Popup.Logger)

	ctx, cancel := context.WithTimeout(ctx, timeouts.Overall)
	defer cancel()

	dir, releaseWorkspace, err := l.Popup.Workspace.open(logger)
	if err != nil {
		return err
	}
	// Given back last: the handshake FIFOs live in it, and the popup is dismissed
	// over one of them on the way out.
	defer releaseWorkspace()

	input, err := newAssuanInput(ctx, cmp.Or[io.Reader](l.stdin, os.Stdin))
	if err != nil {
		return err
	}
	defer func() {
		// A relay that ended because the exchange ended has nothing to add to what
		// the exchange already reported.
		if cerr := input.close(); err == nil {
			err = cerr
		}
	}()

	exchange := &pinentryExchange{
		rendezvous: &popupTTYHandshake{
			backend:        handshaker,
			launcher:       l.Popup,
			logger:         logger,
			dir:            dir,
			readTimeout:    timeouts.TTYRead,
			dismissTimeout: timeouts.DoneWrite,
		},
		pinentry: &pinentryCommand{
			path:   cmp.Or(l.PinentryPath, def.PinentryPath),
			args:   l.PinentryArgs,
			stdout: cmp.Or[io.Writer](l.stdout, os.Stdout),
			stderr: cmp.Or[io.Writer](l.stderr, os.Stderr),
		},
		input:  input.end,
		logger: logger,
	}
	return exchange.run(ctx)
}

// pinentryExchange is one proxied Assuan exchange: the popup announcing a
// terminal, the pinentry process drawing on it, and the rewriting that points
// the one at the other.
//
// It holds no OS stdio and starts no process of its own — both arrive as
// interfaces — so the order it runs them in can be driven by a fake popup and a
// fake process.
type pinentryExchange struct {
	rendezvous ttyRendezvous
	pinentry   pinentryProcess
	// input is the Assuan stream gpg-agent sends. Closing it is how the exchange
	// ends the relay; the stream behind it stays open for whoever owns it.
	input  io.ReadCloser
	logger *slog.Logger
}

func (e *pinentryExchange) run(ctx context.Context) (err error) {
	tty, acquireErr := e.rendezvous.acquire(ctx)
	// Registered even when the acquisition failed: a popup that is open but never
	// announced anything still has to be told to go away, or it lingers until the
	// overall timeout kills it. Failing to dismiss it is worth reporting — but
	// never worth hiding a pinentry failure, so it only becomes the returned error
	// when there is none.
	defer func() {
		if derr := e.rendezvous.dismiss(); derr != nil {
			e.logger.Warn("dismissing the popup failed", slog.Any("err", derr))
			err = cmp.Or(err, derr)
		}
	}()
	if acquireErr != nil {
		return acquireErr
	}

	pinentryInput, err := e.pinentry.start(ctx)
	if err != nil {
		return err
	}
	e.logger.Debug("pinentry started", slog.String("tty", tty))

	relay := new(errgroup.Group)
	relay.Go(func() error {
		defer pinentryInput.Close()
		return rewriteAssuanTTY(e.input, pinentryInput, tty)
	})

	waitErr := e.pinentry.wait()
	e.logger.Debug("pinentry finished", slog.Any("err", waitErr))

	// Pinentry may exit while gpg-agent still holds its end of the input open.
	// Closing this derived end releases the relay in that case; the resulting
	// closed-pipe or cancellation error only records that teardown. Any other
	// relay failure ended the exchange on its own and is worth reporting.
	if cerr := e.input.Close(); cerr != nil {
		e.logger.Debug("closing the assuan input", slog.Any("err", cerr))
	}
	rerr := relay.Wait()
	if rerr != nil {
		e.logger.Debug("assuan relay ended", slog.Any("err", rerr))
	}
	rerr = relayFailure(rerr)
	if errors.Is(rerr, io.ErrClosedPipe) ||
		errors.Is(rerr, context.Canceled) ||
		errors.Is(rerr, context.DeadlineExceeded) {
		rerr = nil
	}
	return cmp.Or(waitErr, rerr)
}

// pinentryProcess is the pinentry binary the exchange drives.
type pinentryProcess interface {
	// start runs pinentry and returns the endpoint its Assuan input goes to.
	// The caller closes the endpoint when the relayed input ends. Canceling ctx
	// ends the process.
	start(ctx context.Context) (io.WriteCloser, error)
	// wait waits for it to exit and reports, naming the binary, how it failed. It
	// also ends the input endpoint start handed out: a process that is gone has
	// nothing left to be told.
	wait() error
}

// pinentryCancelGrace is how long pinentry has to put the terminal it drew on
// back the way it found it after a canceled exchange asked it to stop. It is
// killed afterwards: a pinentry that ignores the interrupt would otherwise
// outlive the exchange that started it, still holding the popup's terminal.
const pinentryCancelGrace = 2 * time.Second

// pinentryCommand runs the pinentry binary outside the popup, on the terminal
// the popup announced.
type pinentryCommand struct {
	path string
	args []string
	// stdout and stderr are where pinentry's own output goes — this process's own
	// stdout is the Assuan channel gpg-agent reads, so what pinentry answers has
	// to arrive there unchanged.
	//
	// They are the endpoints themselves rather than anything wrapped around them,
	// so os/exec hands the descriptors straight to the child when they are files,
	// which is what they are whenever gpg-agent is on the other end. The child then
	// answers on the very descriptor gpg-agent is reading: nothing in this process
	// stands between the two, and a gpg-agent that has already hung up breaks the
	// pipe under the pinentry it stopped listening to rather than under the proxy
	// that still has a popup to dismiss.
	stdout, stderr io.Writer

	cmd *exec.Cmd
}

func (c *pinentryCommand) start(ctx context.Context) (io.WriteCloser, error) {
	cmd := exec.CommandContext(ctx, c.path, c.args...)
	// SIGINT rather than the default kill: it is what a user pressing ctrl-c at
	// the prompt sends, so pinentry ends the way it is built to end.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGINT) }
	cmd.WaitDelay = pinentryCancelGrace
	cmd.Stdout, cmd.Stderr = c.stdout, c.stderr

	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s failed to start: %w", c.path, err)
	}
	c.cmd = cmd
	return input, nil
}

func (c *pinentryCommand) wait() error {
	if err := c.cmd.Wait(); err != nil {
		return fmt.Errorf("%s failed: %w", c.path, err)
	}
	return nil
}
