// Package runinpopup implements the run-in-popup service backing the binary of
// the same name.
package runinpopup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/shellargv"
)

const (
	// defaultPopupStartupTimeout bounds the rendezvous on a payload FIFO, never
	// what the payload then does with it: it covers a shell and a binary starting
	// inside a popup, which is why it can be this generous and still catch a
	// popup that never ran the payload at all.
	defaultPopupStartupTimeout = 30 * time.Second
	// defaultWorkspacePrefix names the directories this package creates in
	// os.TempDir() after the binary they belong to.
	defaultWorkspacePrefix = "run-in-popup-"
	// fifoOpenRetryInterval paces the wait for the payload's read end. Only the
	// write side needs it: opening a FIFO for reading can block until the peer
	// arrives, opening it for writing cannot without wedging on a peer that never
	// comes.
	fifoOpenRetryInterval = 20 * time.Millisecond
)

// WorkspaceOptions configures the directory holding a launch's payload FIFOs.
type WorkspaceOptions struct {
	// Dir, when non-empty, is the work directory holding the FIFOs; the caller
	// owns its lifetime and the launch never removes it. Empty means the launch
	// creates a mode-0700 directory under os.TempDir() and removes it on
	// completion.
	Dir string
	// NamePrefix prefixes the name of a launch-created directory. Empty means
	// "run-in-popup-". Ignored when Dir is set.
	NamePrefix string
	// Retain keeps a launch-created directory after completion, for debugging.
	// The retained path is reported through the launcher's Logger. Ignored when
	// Dir is set.
	Retain bool
}

// open returns the work directory and the func giving it back.
func (o WorkspaceOptions) open(logger *slog.Logger) (dir string, release func(), err error) {
	if o.Dir != "" {
		return o.Dir, func() {}, nil
	}
	dir, err = os.MkdirTemp("", cmp.Or(o.NamePrefix, defaultWorkspacePrefix))
	if err != nil {
		return "", nil, fmt.Errorf("creating the popup workspace: %w", err)
	}
	return dir, func() {
		if o.Retain {
			logger.Info("popup workspace retained", slog.String("dir", dir))
			return
		}
		if err := os.RemoveAll(dir); err != nil {
			logger.Warn("removing the popup workspace failed", slog.Any("err", err))
		}
	}, nil
}

// PopupStreams is the payload's stdio endpoint set, passed per launch rather
// than carried by the reusable PopupSpec template.
//
// The rule is uniform across all three streams: nil means no allocation and
// that stdio stays on the popup's terminal, where the user sees it and can type
// at it; non-nil means a FIFO is allocated in the launch's workspace, redirected
// into the popup's command line, and connected to the given endpoint. The launch
// closes every endpoint it was handed once its stream has ended; when Exec
// itself returns an error the endpoints were never adopted and stay the
// caller's to close.
type PopupStreams struct {
	Stdin  io.ReadCloser
	Stdout io.WriteCloser
	Stderr io.WriteCloser

	// StdoutPipe and StderrPipe request a piped reader for that stream, returned
	// by PopupCommand.StdoutPipe/StderrPipe — os/exec style, for callers that
	// want to read rather than to be written to. A non-nil endpoint above takes
	// priority over the corresponding request.
	StdoutPipe bool
	StderrPipe bool
}

// PopupLauncher opens popups through a Backend. It owns everything a launch
// needs beyond the payload itself: the workspace holding the payload's FIFOs,
// the multiplexer state Backend.Prepare adjusts, and the goroutines relaying
// each FIFO to its endpoint.
type PopupLauncher struct {
	// Backend opens the popups. Required.
	Backend Backend
	// Logger receives progress logs. nil discards them.
	Logger *slog.Logger
	// Workspace configures the directory holding the payload FIFOs. It is only
	// consulted by launches that allocate one.
	Workspace WorkspaceOptions
	// StartupTimeout bounds the rendezvous on each payload FIFO — how long the
	// popup has to reach the payload and open its end. Zero means 30s.
	StartupTimeout time.Duration
}

// Exec opens a popup running spec and returns as soon as it has been launched.
//
// This is launch-level "execute": it allocates a FIFO for each stream named in
// streams, completes the spec into the one-shot LaunchSpec that redirects the
// payload's stdio into those FIFOs, prepares the multiplexer, launches, and
// starts relaying the FIFOs to their endpoints. A launch naming no stream at all
// allocates no workspace and leaves the spec's command line untouched.
//
// The returned command owns what the launch took, and gives it back when its
// Wait returns.
func (l *PopupLauncher) Exec(
	ctx context.Context,
	spec PopupSpec,
	streams PopupStreams,
) (_ *PopupCommand, err error) {
	if l.Backend == nil {
		return nil, errors.New("PopupLauncher.Backend must be set")
	}
	logger := loggerOrDiscard(l.Logger)

	// Undone in reverse on the way out of a launch that never happened; a launch
	// that does happen hands the same funcs to the PopupCommand.
	var rollback []func()
	defer func() {
		if err == nil {
			return
		}
		for _, undo := range slices.Backward(rollback) {
			undo()
		}
	}()

	restore, err := l.Backend.Prepare(ctx)
	if err != nil {
		return nil, fmt.Errorf("backend %s: preparing popup: %w", l.Backend.Name(), err)
	}
	if restore != nil {
		// Best-effort and detached from ctx, so a timed-out or canceled popup
		// still gets the multiplexer state back, and its failure is logged rather
		// than replacing whatever the caller was really doing.
		restoreCtx := context.WithoutCancel(ctx)
		rollback = append(rollback, func() {
			if err := restore(restoreCtx); err != nil {
				logger.Warn(
					"restoring multiplexer state failed",
					slog.String("backend", l.Backend.Name()),
					slog.Any("err", err),
				)
			}
		})
	}

	set, stdoutPipe, stderrPipe := payloadStreams(streams)
	if len(set) > 0 {
		dir, releaseWorkspace, err := l.Workspace.open(logger)
		if err != nil {
			return nil, err
		}
		rollback = append(rollback, releaseWorkspace)
		for _, s := range set {
			s.path = filepath.Join(dir, s.name)
			if err := syscall.Mknod(s.path, syscall.S_IFIFO|0o600, 0); err != nil {
				return nil, fmt.Errorf("creating fifo %q: %w", s.path, err)
			}
		}
	}

	command, script := launchCommandLine(spec, set)
	launchSpec := LaunchSpec{
		Title:   spec.Title,
		Env:     spec.Env,
		Command: command,
		Script:  script,
	}
	for _, s := range set {
		s.wire(&launchSpec)
	}

	logger.Debug(
		"launching popup",
		slog.String("backend", l.Backend.Name()),
		slog.Any("command", command),
		slog.String("script", script),
	)

	launchCtx, cancelLaunch := context.WithCancel(ctx)
	rollback = append(rollback, cancelLaunch)

	handle, err := l.Backend.Launch(launchCtx, launchSpec)
	if err != nil {
		return nil, fmt.Errorf("popup failed: %w", err)
	}

	startupTimeout := cmp.Or(l.StartupTimeout, defaultPopupStartupTimeout)
	cmd := &PopupCommand{
		endpoints:  new(errgroup.Group),
		piped:      new(errgroup.Group),
		stdoutPipe: stdoutPipe,
		stderrPipe: stderrPipe,
		waitLauncher: sync.OnceValue(func() error {
			if err := handle.Wait(); err != nil {
				return fmt.Errorf("popup failed: %w", err)
			}
			return nil
		}),
	}
	// Releasing dismisses the popup and undoes the launch, in reverse. Reaping
	// the dismissed launcher is left to a goroutine on purpose: a popup that
	// takes its time going away must not hold up the multiplexer state waiting to
	// be restored, and by then its exit is only wanted for the diagnostics it
	// carries.
	cmd.release = sync.OnceFunc(func() {
		cancelLaunch()
		go func() {
			if err := cmd.waitLauncher(); err != nil {
				logger.Debug("popup launcher exited", slog.Any("err", err))
			}
		}()
		if err := cmd.piped.Wait(); err != nil {
			logger.Debug("popup payload stream ended", slog.Any("err", err))
		}
		for _, undo := range slices.Backward(rollback) {
			undo()
		}
	})
	for _, s := range set {
		group := cmd.endpoints
		if s.pipe != nil {
			group = cmd.piped
		}
		group.Go(func() error { return s.pump(launchCtx, startupTimeout) })
	}
	return cmd, nil
}

// PopupCommand is a launched popup: the launcher process, the goroutines
// relaying the payload's stdio, and everything the launch has to give back once
// they are done.
type PopupCommand struct {
	// waitLauncher is memoized: the launcher is waited on from Wait, from the
	// release and — for exchanges that outlive it — from the caller's own
	// failure watch, but a process can only be reaped once.
	waitLauncher func() error
	// endpoints relays the streams the caller handed an endpoint for; piped
	// relays the ones it reads itself.
	endpoints, piped *errgroup.Group
	stdoutPipe       io.ReadCloser
	stderrPipe       io.ReadCloser
	// release dismisses the popup and gives back everything the launch took. It
	// runs exactly once, however the command ends.
	release func()
}

// Wait waits for the popup launcher to exit and for every stream relayed into a
// caller-supplied endpoint, then dismisses the popup and gives back what the
// launch took.
//
// The launcher exiting is not the payload finishing — the floating-pane
// mechanisms return as soon as the pane exists — so a caller that needs to know
// when the payload itself is done has to arrange for the payload to tell it, the
// way the pinentry handshake does over its own FIFOs.
//
// A launcher that failed before the popup ever reached the payload leaves the
// wait for an allocated stream to the launcher's startup bound; it is the caller
// above, which knows what the payload owes it, that can end such a wait earlier.
func (c *PopupCommand) Wait() error {
	err := c.waitLauncher()
	if perr := c.endpoints.Wait(); err == nil {
		err = perr
	}
	c.release()
	return err
}

// WaitStreams waits like Wait, but lets the payload's streams say how the launch
// went: it reports nothing when every stream relayed into a caller-supplied
// endpoint ran to its end, however the launcher then exited.
//
// That is the difference, and it is what a caller relaying a payload's output
// wants. tmux display-popup's launcher carries the payload's exit status, so
// Wait would turn a payload that exited non-zero into a failed launch, while the
// floating-pane mechanisms — whose launcher is long gone by then — would call
// the same run a success. The launcher's failure is still reported when a stream
// failed too: a popup that died is why the stream ended, and explains it better
// than the stream can.
func (c *PopupCommand) WaitStreams() error {
	streamErr := c.endpoints.Wait()
	launcherErr := c.waitLauncher()
	c.release()
	if streamErr == nil {
		return nil
	}
	return cmp.Or(launcherErr, streamErr)
}

// StdoutPipe returns the reader allocated when PopupStreams.StdoutPipe was set,
// and false when piping was not requested — the flag was unset, or a non-nil
// Stdout endpoint overrode it.
//
// As with os/exec, read the reader to EOF before calling Wait: Wait dismisses
// the popup, and a relay still draining the FIFO ends with that dismissal as its
// error rather than with the payload's own EOF.
func (c *PopupCommand) StdoutPipe() (io.ReadCloser, bool) {
	return c.stdoutPipe, c.stdoutPipe != nil
}

// StderrPipe returns the reader allocated when PopupStreams.StderrPipe was set,
// under the same rules as StdoutPipe.
func (c *PopupCommand) StderrPipe() (io.ReadCloser, bool) {
	return c.stderrPipe, c.stderrPipe != nil
}

// The payload's stdio streams, named as they appear in an error and as their
// FIFO is named in the workspace.
const (
	streamStdin  = "stdin"
	streamStdout = "stdout"
	streamStderr = "stderr"
)

// popupStream is one payload stdio stream, the FIFO standing in for it inside
// the popup and the endpoint it is relayed to out here.
type popupStream struct {
	name string
	path string
	// redirect is the shell operator connecting the payload's descriptor to the
	// FIFO.
	redirect string
	// src is set for the payload's stdin, dst for its stdout and stderr.
	src io.ReadCloser
	dst io.WriteCloser
	// pipe is dst again when dst is the write half of a reader handed to the
	// caller, whose Read has to be told why a stream ended.
	pipe *io.PipeWriter
}

// payloadStreams turns the requested endpoints into the streams a launch
// allocates, plus the readers PopupCommand hands out. A non-nil endpoint wins
// over its pipe request; a stream neither of them names is not allocated at all.
func payloadStreams(streams PopupStreams) (set []*popupStream, stdout, stderr io.ReadCloser) {
	if streams.Stdin != nil {
		set = append(set, &popupStream{name: streamStdin, redirect: "<", src: streams.Stdin})
	}
	out, stdout := outStream(streamStdout, ">", streams.Stdout, streams.StdoutPipe)
	if out != nil {
		set = append(set, out)
	}
	errOut, stderr := outStream(streamStderr, "2>", streams.Stderr, streams.StderrPipe)
	if errOut != nil {
		set = append(set, errOut)
	}
	return set, stdout, stderr
}

// wire attaches the stream's endpoint to the one-shot spec, so the backend sees
// what each of the payload's descriptors ends up connected to — a caller's
// endpoint, or the pipe whose reader the caller was handed.
func (s *popupStream) wire(spec *LaunchSpec) {
	switch s.name {
	case streamStdin:
		spec.Stdin = s.src
	case streamStdout:
		spec.Stdout = s.dst
	case streamStderr:
		spec.Stderr = s.dst
	}
}

func outStream(
	name, redirect string,
	endpoint io.WriteCloser,
	piped bool,
) (*popupStream, io.ReadCloser) {
	switch {
	case endpoint != nil:
		return &popupStream{name: name, redirect: redirect, dst: endpoint}, nil
	case piped:
		r, w := io.Pipe()
		return &popupStream{name: name, redirect: redirect, dst: w, pipe: w}, r
	default:
		return nil, nil
	}
}

// launchCommandLine folds the allocated FIFOs into the popup's command line.
//
// The payload is wrapped in a group so that a redirection covers all of it, and
// not just the last command of a Script. Without allocated streams the spec's
// own argv is handed over untouched: a backend able to run an argv directly must
// not be pushed through a shell for nothing.
func launchCommandLine(spec PopupSpec, set []*popupStream) (command []string, script string) {
	if len(set) == 0 {
		return spec.Command, spec.Script
	}
	payload := spec.Script
	if payload == "" {
		payload = shellargv.Join(spec.Command)
	}
	var sb strings.Builder
	// The newline is what makes the closing brace a command of its own, whatever
	// the payload ends with.
	fmt.Fprintf(&sb, "{ %s\n}", payload)
	for _, s := range set {
		fmt.Fprintf(&sb, " %s %s", s.redirect, shellargv.Quote(s.path))
	}
	return nil, sb.String()
}

// pump relays one stream between its FIFO and its endpoint, and closes the
// endpoint with whatever ended the stream.
func (s *popupStream) pump(ctx context.Context, startupTimeout time.Duration) error {
	if s.src != nil {
		return s.pumpIn(ctx, startupTimeout)
	}
	return s.pumpOut(ctx, startupTimeout)
}

func (s *popupStream) pumpOut(ctx context.Context, startupTimeout time.Duration) (err error) {
	defer func() {
		if cerr := s.closeDst(err); err == nil {
			err = cerr
		}
	}()

	f, err := openFifoReader(ctx, s.path, startupTimeout)
	if err != nil {
		return err
	}
	defer f.Close()

	// Cancellation has to reach both ends: the read, which is otherwise blocked
	// until the payload writes, and a caller's Read blocked on the pipe.
	stop := context.AfterFunc(ctx, func() {
		_ = f.SetReadDeadline(time.Now())
		if s.pipe != nil {
			_ = s.pipe.CloseWithError(context.Cause(ctx))
		}
	})
	defer stop()

	if _, err := io.Copy(s.dst, f); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return fmt.Errorf("relaying the popup payload's %s: %w", s.name, err)
	}
	return nil
}

func (s *popupStream) pumpIn(ctx context.Context, startupTimeout time.Duration) (err error) {
	defer func() {
		if cerr := s.src.Close(); err == nil {
			err = cerr
		}
	}()

	f, err := openFifoWriter(ctx, s.path, startupTimeout)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = f.SetWriteDeadline(time.Now()) })
	defer stop()

	_, err = io.Copy(f, s.src)
	// Closed here rather than in a defer: the close is what gives the payload its
	// EOF, so a close that fails is input the payload may wait for forever.
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return fmt.Errorf("relaying the popup payload's %s: %w", s.name, err)
	}
	return nil
}

// closeDst ends the endpoint's side of the stream. A caller reading a pipe is
// blocked in Read and learns the reason from the close; a caller that handed
// over a writer only needs it closed.
func (s *popupStream) closeDst(cause error) error {
	if s.pipe != nil {
		return s.pipe.CloseWithError(cause)
	}
	return s.dst.Close()
}

// openFifoReader opens the read end of a payload FIFO. The open blocks until the
// payload opens its write end, which is what makes it the rendezvous — and what
// means only ctx or startupTimeout can end it when the payload never arrives. A
// blocked open is woken by opening the same FIFO read-write, which succeeds with
// nobody on the other side.
func openFifoReader(
	ctx context.Context,
	path string,
	startupTimeout time.Duration,
) (*os.File, error) {
	type opened struct {
		f   *os.File
		err error
	}
	ch := make(chan opened, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_RDONLY, 0)
		ch <- opened{f, err}
	}()

	var abort error
	select {
	case o := <-ch:
		if o.err != nil {
			return nil, fmt.Errorf("opening fifo %q: %w", path, o.err)
		}
		return o.f, nil
	case <-time.After(startupTimeout):
		abort = fifoStartupError(path, startupTimeout)
	case <-ctx.Done():
		abort = context.Cause(ctx)
	}

	wake, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		// Nothing left to try: the open stays blocked until something else opens
		// the fifo, and its goroutine with it.
		return nil, abort
	}
	defer wake.Close()
	if o := <-ch; o.f != nil {
		o.f.Close()
	}
	return nil, abort
}

// openFifoWriter opens the write end of a payload FIFO. O_NONBLOCK because the
// blocking form would wedge here forever when the payload never reads; ENXIO is
// exactly the "no reader yet" case, so it is the one retried.
func openFifoWriter(
	ctx context.Context,
	path string,
	startupTimeout time.Duration,
) (*os.File, error) {
	deadline := time.Now().Add(startupTimeout)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.ENXIO) {
			return nil, fmt.Errorf("opening fifo %q: %w", path, err)
		}
		if time.Now().After(deadline) {
			return nil, fifoStartupError(path, startupTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-time.After(fifoOpenRetryInterval):
		}
	}
}

func fifoStartupError(path string, startupTimeout time.Duration) error {
	return fmt.Errorf(
		"the popup did not reach its payload within %v: nothing opened %q",
		startupTimeout, path,
	)
}

func loggerOrDiscard(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.DiscardHandler)
}
