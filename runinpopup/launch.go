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

	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/fifo"
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
	// popupDismissTimeout bounds closing the popup of a canceled launch. It is
	// this generous because a dismissal may first have to sit out the launcher it
	// reads a pane id from: the same cancellation interrupts that launcher, and
	// the backends give an interrupted one a couple of seconds to go away before
	// killing it.
	popupDismissTimeout = 5 * time.Second
)

// WorkspaceOptions configures a launch's work directory: the payload FIFOs and
// whatever the backend puts beside them.
type WorkspaceOptions struct {
	// Dir, when non-empty, is the work directory holding the FIFOs; the caller
	// owns its lifetime and the launch never removes it. It must be a directory
	// owned by the calling user and writable by nobody else, and a launch
	// refuses one that is not: whoever can write the directory can replace a
	// FIFO in it with one of their own, and the pinentry handshake reads the
	// terminal that gets the passphrase prompt off such a FIFO. Empty means the
	// launch creates a mode-0700 directory under os.TempDir() and removes it on
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
		if err := checkWorkspace(o.Dir); err != nil {
			return "", nil, err
		}
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

// checkWorkspace enforces WorkspaceOptions.Dir's ownership and permission
// contract on a caller-provided directory; a launch-created one is born mode
// 0700 and needs no checking. The directory itself is all that is looked at:
// its ancestors are commonly sticky world-writable temp directories, where a
// FIFO cannot be replaced by anyone but its owner.
func checkWorkspace(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("checking the popup workspace: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("popup workspace %q is not a directory", dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("popup workspace %q is not owned by this user", dir)
	}
	if perm := info.Mode().Perm(); perm&0o022 != 0 {
		return fmt.Errorf(
			"popup workspace %q is writable by other users (mode %04o)", dir, perm,
		)
	}
	return nil
}

// PopupStreams is the payload's stdio endpoint set, passed per launch rather
// than carried by the reusable PopupSpec template.
//
// The rule is uniform across all three streams: nil means no allocation and
// that stdio stays on the popup's terminal, where the user sees it and can type
// at it; non-nil means a FIFO is allocated in the launch's workspace, attached to
// the payload by the popup's command line, and connected to the given endpoint.
// The launch closes every endpoint it was handed once its stream has ended; when
// Exec itself returns an error the endpoints were never adopted and stay the
// caller's to close.
//
// Stdin is relayed but never waited on. Its relay sits in a read on the source
// it was handed, which only whoever owns that source can end — a caller's
// terminal never does — so no Wait may depend on it; it ends when the source
// does, or on the first write past the popup's death, and Stdin is closed then
// rather than when the launch is over.
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

	// KeepStdio attaches the allocated FIFOs beside the payload's stdio instead
	// of in place of it. Its fd 0, 1 and 2 stay on the popup's terminal, and each
	// allocated FIFO arrives on a descriptor of its own — the stdin one on fd 3
	// open for reading, the stdout one on fd 4 and the stderr one on fd 5 open for
	// writing — with its path in TTY_IN, TTY_OUT or TTY_ERR for a payload that
	// cannot inherit descriptors and has to open the FIFO by name. A stream that
	// was not allocated gets neither its descriptor nor its variable.
	//
	// Only the payload sees the difference: the endpoints and the pipe requests
	// above behave the same either way, and a launch naming no stream at all is
	// untouched by this.
	//
	// Which way round a launch wants depends on whose stdio it is. A payload
	// speaking a protocol over its stdout is written for the streams and wants
	// them taken over; an ordinary terminal program is not, and drawing in the
	// popup as it would anywhere is the whole reason it was put there.
	KeepStdio bool
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
	// Workspace configures the launch's work directory. It is only consulted by
	// launches that need one.
	Workspace WorkspaceOptions
	// StartupTimeout bounds the rendezvous on each payload FIFO — how long the
	// popup has to reach the payload and open its end. Zero means 30s.
	StartupTimeout time.Duration
}

// Exec opens a popup running spec and returns as soon as it has been launched.
//
// This is launch-level "execute": it allocates a FIFO for each stream named in
// streams, completes the spec into the one-shot LaunchSpec that attaches those
// FIFOs to the payload, prepares the multiplexer, launches, and starts relaying
// the FIFOs to their endpoints. A launch naming no stream leaves
// the spec's command line untouched, and one needing neither a stream nor a work
// directory for its environment opens no workspace at all.
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
	// Before anything is opened, prepared or allocated: a geometry nobody can act
	// on is the caller's typo, and it must cost no popup to find out.
	if err := spec.validateGeometry(); err != nil {
		return nil, err
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
	// An environment opens the directory too: a backend whose multiplexer has no
	// environment flag has nowhere but the workspace to put one out of the argv.
	// Asked here rather than of the backend, so a spec's needs alone decide what
	// a launch allocates; the tmux backends have their flag and leave the
	// directory empty, which is cheaper than negotiating with every backend.
	var workDir string
	if len(set) > 0 || len(spec.Env) > 0 {
		dir, releaseWorkspace, err := l.Workspace.open(logger)
		if err != nil {
			return nil, err
		}
		rollback = append(rollback, releaseWorkspace)
		workDir = dir
		for _, s := range set {
			s.path = filepath.Join(dir, s.name)
			if err := fifo.Mkfifo(s.path); err != nil {
				return nil, err
			}
		}
	}

	startupTimeout := cmp.Or(l.StartupTimeout, defaultPopupStartupTimeout)
	command, script := launchCommandLine(spec, set)
	launchSpec := LaunchSpec{
		Title:          spec.Title,
		Env:            spec.Env,
		X:              spec.X,
		Y:              spec.Y,
		Width:          spec.Width,
		Height:         spec.Height,
		Command:        command,
		Script:         script,
		WorkDir:        workDir,
		StartupTimeout: startupTimeout,
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

	// Canceling a launch closes its popup, rather than merely interrupting the
	// launcher: the launcher process is not the popup, and interrupting it leaves
	// tmux's display-popup — and the payload inside it — running while only the
	// client displaying it goes away. The interrupt the cancellation also sends
	// stays as the backstop for a dismissal that fails or never returns.
	//
	// The dismissal runs on a context of its own because the launch's is the very
	// thing that was canceled, and bounded so a multiplexer that will not answer
	// cannot hold the launch open. What it reports is logged: the launch is ending
	// on its cancellation, not on this.
	dismiss := sync.OnceFunc(func() {
		dismissCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			popupDismissTimeout,
		)
		defer cancel()
		if err := handle.Dismiss(dismissCtx); err != nil {
			logger.Warn(
				"dismissing the popup failed",
				slog.String("backend", l.Backend.Name()),
				slog.Any("err", err),
			)
		}
	})
	stopDismiss := context.AfterFunc(ctx, dismiss)

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
		// Only a canceled launch is closed from here. One that ended on its own
		// leaves the popup alone — exec's closes itself when the command exits, and
		// the exchanges that outlive their launcher dismiss theirs the way their
		// payload agreed to — and stopping the watch is also what keeps a
		// cancellation arriving afterwards, when a caller's context is finally let
		// go of, from closing a popup that is long gone. Losing that stop to a
		// cancellation already under way is what the second half is for: whichever
		// of the two noticed first, this returns with the popup dismissed.
		if !stopDismiss() || ctx.Err() != nil {
			dismiss()
		}
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
		if s.src != nil {
			// Started rather than joined, and it cannot be otherwise: this relay
			// spends the launch parked in a read on the caller's source, which only
			// that caller can end and which this package must not close its way out
			// of. A wait that included it would outlast the popup it was feeding by
			// however long the caller keeps typing nothing.
			go func() {
				if err := s.pump(launchCtx, startupTimeout); err != nil {
					logger.Debug("popup payload input relay ended", slog.Any("err", err))
				}
			}()
			continue
		}
		group := cmd.endpoints
		if s.pipe != nil {
			group = cmd.piped
		} else {
			cmd.hasEndpoints = true
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
	// endpoints relays the output streams the caller handed an endpoint for;
	// piped relays the ones it reads itself. The input relay is in neither: see
	// PopupStreams for why nothing here waits on it.
	endpoints, piped *errgroup.Group
	// hasEndpoints says the endpoints group has streams to wait for, which is
	// what WaitStreams has a verdict by.
	hasEndpoints bool
	stdoutPipe   io.ReadCloser
	stderrPipe   io.ReadCloser
	// release dismisses the popup and gives back everything the launch took. It
	// runs exactly once, however the command ends.
	release func()
}

// Wait waits for the popup launcher to exit and for every output stream relayed
// into a caller-supplied endpoint, then dismisses the popup and gives back what
// the launch took. The payload's input is not among them — PopupStreams says why
// nothing waits on that relay.
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
// went: it reports nothing when every output stream relayed into a
// caller-supplied endpoint ran to its end, however the launcher then exited.
//
// That is the difference, and it is what a caller relaying a payload's output
// wants. tmux display-popup's launcher carries the payload's exit status, so
// Wait would turn a payload that exited non-zero into a failed launch, while the
// floating-pane mechanisms — whose launcher is long gone by then — would call
// the same run a success. The launcher's failure is still reported when a stream
// failed too: a popup that died is why the stream ended, and explains it better
// than the stream can.
//
// A launch that named no output endpoint — one that only feeds the payload's
// stdin, or only requested pipes, included — has nothing to decide by: every
// verdict here would be vacuously clean, launcher failure included, so such a
// launch waits through Wait. A piped stream is not an endpoint here on purpose:
// its verdict is whatever the caller's own read of it saw.
func (c *PopupCommand) WaitStreams() error {
	if !c.hasEndpoints {
		return c.Wait()
	}
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

// attachment is how one FIFO reaches the payload: the shell operator putting it
// on a descriptor, and the variable naming its path — empty for a FIFO that took
// the stdio it stands for, since a payload reading its own stdin was never told a
// path either.
type attachment struct {
	redirect string
	envName  string
}

// attachments says how the three FIFOs of a launch reach its payload, under
// PopupStreams.KeepStdio.
func attachments(keepStdio bool) (in, out, errOut attachment) {
	if keepStdio {
		return attachment{"3<", "TTY_IN"}, attachment{"4>", "TTY_OUT"}, attachment{"5>", "TTY_ERR"}
	}
	return attachment{redirect: "<"}, attachment{redirect: ">"}, attachment{redirect: "2>"}
}

// popupStream is one payload stdio stream, the FIFO standing in for it inside
// the popup and the endpoint it is relayed to out here.
type popupStream struct {
	attachment
	name string
	path string
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
	inAt, outAt, errAt := attachments(streams.KeepStdio)
	if streams.Stdin != nil {
		set = append(set, &popupStream{
			attachment: inAt,
			name:       streamStdin,
			src:        streams.Stdin,
		})
	}
	out, stdout := outStream(streamStdout, outAt, streams.Stdout, streams.StdoutPipe)
	if out != nil {
		set = append(set, out)
	}
	errOut, stderr := outStream(streamStderr, errAt, streams.Stderr, streams.StderrPipe)
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
	name string,
	at attachment,
	endpoint io.WriteCloser,
	piped bool,
) (*popupStream, io.ReadCloser) {
	switch {
	case endpoint != nil:
		return &popupStream{attachment: at, name: name, dst: endpoint}, nil
	case piped:
		r, w := io.Pipe()
		return &popupStream{attachment: at, name: name, dst: w, pipe: w}, r
	default:
		return nil, nil
	}
}

// launchCommandLine folds the allocated FIFOs into the popup's command line.
//
// The payload is wrapped in a group so that a redirection covers all of it, and
// not just the last command of a Script, and whatever names the FIFOs is exported
// ahead of that group. Without allocated streams the spec's own argv is handed
// over untouched: nothing is being attached to the payload, and a backend able to
// run an argv directly must not be pushed through a shell for nothing.
//
// The group's redirections are what open the FIFOs inside the popup, on the way
// into the group and whichever descriptors they land on, so the rendezvous with
// the relays out here is the same either way — as is the end of it: the group
// exiting closes them, and that is the payload's EOF.
func launchCommandLine(spec PopupSpec, set []*popupStream) (command []string, script string) {
	if len(set) == 0 {
		return spec.Command, spec.Script
	}
	payload := spec.Script
	if payload == "" {
		payload = shellargv.Join(spec.Command)
	}
	var sb strings.Builder
	for _, s := range set {
		if s.envName == "" {
			continue
		}
		if sb.Len() == 0 {
			sb.WriteString("export")
		}
		fmt.Fprintf(&sb, " %s=%s", s.envName, shellargv.Quote(s.path))
	}
	if sb.Len() > 0 {
		sb.WriteString("\n")
	}
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

	f, err := fifo.OpenReader(ctx, s.path, startupTimeout)
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

	f, err := fifo.OpenWriter(ctx, s.path, startupTimeout)
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

func loggerOrDiscard(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.DiscardHandler)
}
