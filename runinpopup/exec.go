package runinpopup

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

// ExecResult is the outcome of one command run inside a popup, as the payload
// reports it back to the caller. The json tags are a documented wire format:
// the exec subcommand prints this value verbatim on its stdout.
type ExecResult struct {
	// Command is the argv the payload ran.
	Command []string `json:"command"`
	// ExitCode is the command's exit status, or -1 when it never started or was
	// killed by a signal — the convention os/exec itself uses.
	ExitCode int `json:"exit_code"`
	// Stdout and Stderr are everything the command wrote, captured in full while
	// it was drawing on the popup terminal.
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	// Error says why the command could not be run to a known status: it never
	// started, or its output could not be relayed. It stays empty whenever the
	// command ran and ExitCode is its own, however it exited.
	Error string `json:"error,omitempty"`
}

// ExecOutcome is what ExecPayload hands back to the popup process it runs in —
// as opposed to ExecResult, which is what travels to the caller.
type ExecOutcome struct {
	// Result is what was reported over the fifo. It is the zero value when Ran is
	// false.
	Result ExecResult
	// Ran reports whether the command was attempted at all. It is false only when
	// the caller was already gone before anything could be tried, so there is no
	// status to stand in for.
	Ran bool
	// Status is the exit status the popup process should carry, so the
	// multiplexer sees what the command did. It is Result.ExitCode, except that a
	// signal death — -1 in the JSON, which no exit(2) can carry — becomes
	// 128+signal, and anything else unusable becomes 1.
	Status int
}

// ExecPayloadCommandName is the subcommand CallExec re-invokes the payload
// executable with, and ExecPayloadStartupTimeoutFlag the one flag it may pass
// along with it. The library owns both names because the library builds the
// argv; the CLI registers a hidden leaf accepting them and calls ExecPayload.
const (
	ExecPayloadCommandName        = "exec-payload"
	ExecPayloadStartupTimeoutFlag = "startup-timeout"
)

const (
	// execLauncherGrace is how long the rendezvous gets after the popup launcher
	// exited with an error. The launcher's exit says little on its own: with tmux
	// display-popup it is also how a *successful* run ends, carrying the payload's
	// own status, while the floating-pane backends exit long before the payload
	// does. Failing the moment it errors would race a payload that is
	// already opening its end, so it only shortens the wait.
	execLauncherGrace = 2 * time.Second
	// defaultExecStartupTimeout bounds the rendezvous, never the command: it
	// covers a shell and a binary starting inside a popup, which is why it can be
	// this generous and still catch a popup that never ran the payload at all.
	defaultExecStartupTimeout = 30 * time.Second
	// execWriterRetryInterval paces the payload's wait for the caller's read end.
	execWriterRetryInterval = 20 * time.Millisecond
	// execResultWriteTimeout bounds handing the finished result over. It guards
	// only a caller that stopped draining the fifo while still holding it open —
	// one that let go fails at once with EPIPE — so it can be generous.
	execResultWriteTimeout = 30 * time.Second
)

// ExecOptions configures CallExec.
type ExecOptions struct {
	// Logger receives progress logs. nil discards them.
	Logger *slog.Logger
	// TempDir is where the result FIFO is created. Required: the caller owns the
	// directory's lifetime, so it can keep it for debugging. It must not be
	// writable by other users, since anything that can open the FIFO can forge
	// the result.
	TempDir string
	// Command is the argv run inside the popup. Required.
	Command []string
	// Title names the popup window or pane. Empty leaves the backend's default;
	// tmux-floating-pane has no title flag and ignores it.
	Title string
	// PayloadPath is the executable the popup re-invokes as
	//
	//	<PayloadPath> exec-payload <result-fifo> -- <Command...>
	//
	// and which must answer that argv by calling ExecPayload. Empty means
	// os.Executable(): this binary re-invoking itself, which is what the CLI
	// wants. Tests point it at a stand-in.
	PayloadPath string
	// StartupTimeout bounds the *rendezvous* only — how long the two halves have
	// to find each other on the fifo. Zero means 30s. A non-default value travels
	// to the payload in its argv, so both halves give up together. The command
	// itself is never bounded by it: once the rendezvous is made, only ctx ends
	// the wait.
	StartupTimeout time.Duration
}

func (o ExecOptions) normalized() (ExecOptions, error) {
	if o.TempDir == "" {
		return o, errors.New("ExecOptions.TempDir must be set")
	}
	if len(o.Command) == 0 {
		return o, errors.New("ExecOptions.Command must not be empty")
	}
	if o.PayloadPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return o, fmt.Errorf("locating this executable for the exec payload: %w", err)
		}
		o.PayloadPath = exe
	}
	o.StartupTimeout = cmp.Or(o.StartupTimeout, defaultExecStartupTimeout)
	return o, nil
}

// CallExec runs opts.Command inside a popup and reports what it did.
//
// The popup runs opts.PayloadPath — by default this executable — as the exec
// payload: it executes the command on the popup's terminal, where the user sees
// it live and can type at it, and writes an ExecResult to a FIFO in opts.TempDir
// when it finishes. CallExec waits on that FIFO, so it returns when the
// *command* is done rather than when the popup launcher is.
//
// The wait has two phases. The rendezvous — the payload opening its end of the
// FIFO, which it does before running anything — is bounded by
// opts.StartupTimeout, since a popup that never reaches the payload would
// otherwise never say so. Everything after it is the command's own runtime and
// is bounded by ctx alone: Config.Timeouts is deliberately not consulted, as it
// sizes a pinentry prompt and any bound tight enough for that would kill the
// long-running commands this exists to run. Once the rendezvous is made, the
// FIFO's EOF is the payload's death certificate — this end holds no writer of
// its own — so a payload that dies without reporting ends the wait instead of
// extending it forever.
//
// A command that exits non-zero is not an error: its status is
// ExecResult.ExitCode. The error return covers the transport only — opening the
// popup, the FIFO, decoding the result.
func CallExec(ctx context.Context, backend Backend, opts ExecOptions) (ExecResult, error) {
	opts, err := opts.normalized()
	if err != nil {
		return ExecResult{}, err
	}
	return callExec(ctx, backend, loggerOrDiscard(opts.Logger), opts)
}

func callExec(
	ctx context.Context,
	backend Backend,
	logger *slog.Logger,
	opts ExecOptions,
) (ExecResult, error) {
	resultFifo := filepath.Join(opts.TempDir, "result")
	if err := syscall.Mknod(resultFifo, syscall.S_IFIFO|0o600, 0); err != nil {
		return ExecResult{}, fmt.Errorf("creating fifo %q: %w", resultFifo, err)
	}
	logger.Debug("result fifo created", slog.String("path", resultFifo))

	launcher := &PopupLauncher{Backend: backend, Logger: logger}
	popup, err := launcher.Exec(
		ctx,
		PopupSpec{
			Title:   opts.Title,
			Command: slices.Concat(execPayloadArgv(opts, resultFifo), opts.Command),
		},
		// No payload stdio is allocated: the command runs on the popup's
		// terminal, where the user sees it and can type at it, and reports itself
		// over the result fifo.
		PopupStreams{},
	)
	if err != nil {
		return ExecResult{}, err
	}
	// The exchange outlives the launcher, so the popup is released here instead
	// of by waiting on it.
	defer popup.release()

	launcherFailed := make(chan error, 1)
	go func() {
		// A launcher that exits successfully says nothing: the floating-pane
		// backends do that while the payload still runs.
		if werr := popup.waitLauncher(); werr != nil {
			launcherFailed <- werr
		}
	}()

	f, err := openResultReader(ctx, logger, resultFifo, launcherFailed, opts.StartupTimeout)
	if err != nil {
		return ExecResult{}, err
	}
	defer f.Close()

	logger.Debug("payload connected, waiting for the exec result")

	stopOnCancel := context.AfterFunc(ctx, func() { _ = f.SetReadDeadline(time.Now()) })
	defer stopOnCancel()

	b, err := io.ReadAll(f)
	if err != nil {
		return ExecResult{}, fmt.Errorf("reading exec result from %q: %w", resultFifo, err)
	}
	if len(b) == 0 {
		// EOF with nothing in it: the payload's end was closed for it, which is
		// what happens when the pane it ran in went away.
		gone := errors.New("the popup payload exited without reporting a result")
		select {
		case werr := <-launcherFailed:
			return ExecResult{}, fmt.Errorf("%w: %w", gone, werr)
		default:
		}
		return ExecResult{}, gone
	}

	var result ExecResult
	if err := json.Unmarshal(b, &result); err != nil {
		return ExecResult{}, fmt.Errorf("decoding the exec result from %q: %w", resultFifo, err)
	}

	logger.Debug(
		"exec result read",
		slog.Any("command", result.Command),
		slog.Int("exit_code", result.ExitCode),
	)
	return result, nil
}

// execPayloadArgv builds everything in the popup's argv up to and including the
// "--" that separates it from the user's command. The startup bound only appears
// when it is not the one the payload would pick anyway, so the ordinary popup
// command line — the one that shows up in ps — stays as short as it was.
func execPayloadArgv(opts ExecOptions, resultFifo string) []string {
	argv := []string{opts.PayloadPath, ExecPayloadCommandName}
	if opts.StartupTimeout != defaultExecStartupTimeout {
		argv = append(argv, fmt.Sprintf(
			"--%s=%s", ExecPayloadStartupTimeoutFlag, opts.StartupTimeout,
		))
	}
	return append(argv, resultFifo, "--")
}

// openResultReader completes the rendezvous with the payload. The payload opens
// its write end before it runs anything, so this open returning is the proof
// that the popup got as far as the payload — and from then on the FIFO's EOF
// belongs to the payload alone, which is why this end must not be opened
// read-write the way the pinentry handshake opens its own FIFOs.
//
// The open is the one step that can block indefinitely, so every way the
// rendezvous can fail has to end it: ctx, a launcher that exited with an error,
// or startupTimeout. A blocked open is woken by opening the same FIFO
// read-write, which succeeds with no writer on the other side.
func openResultReader(
	ctx context.Context,
	logger *slog.Logger,
	path string,
	launcherFailed <-chan error,
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

	var (
		abort       error
		launcherErr error
		grace       <-chan time.Time
		startup     = time.After(startupTimeout)
	)
wait:
	for {
		select {
		case o := <-ch:
			if o.err != nil {
				return nil, fmt.Errorf("opening result fifo %q: %w", path, o.err)
			}
			return o.f, nil
		case launcherErr = <-launcherFailed:
			logger.Debug("popup launcher failed", slog.Any("err", launcherErr))
			grace = time.After(execLauncherGrace)
		case <-grace:
			abort = launcherErr
			break wait
		case <-startup:
			abort = fmt.Errorf(
				"the popup did not reach the exec payload within %v:"+
					" nothing opened %q to report a result",
				startupTimeout, path,
			)
			break wait
		case <-ctx.Done():
			abort = ctx.Err()
			break wait
		}
	}

	wake, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		// Nothing left to try: the open stays blocked until something else opens
		// the fifo, and its goroutine with it.
		logger.Warn("waking the blocked result fifo open failed", slog.Any("err", err))
		return nil, abort
	}
	defer wake.Close()
	if o := <-ch; o.f != nil {
		o.f.Close()
	}
	return nil, abort
}

// ExecPayload is the popup half of CallExec: it runs argv on the popup's
// terminal and reports the outcome on resultFifoPath.
//
// stdin is inherited, so the command can prompt on the popup's tty, and its
// output is teed — live to this process's stdout/stderr, which are the popup,
// and into the result. The tee needs pipes, so the command does not see a tty on
// its stdout: programs that gate color or progress rendering on isatty render
// plainly.
//
// The FIFO is opened for writing *before* the command runs, and held open until
// the result has been written. That is what lets the caller tell a payload that
// died from one that is merely slow, and it is why a caller that has already
// given up is noticed here rather than waited on: when nothing is reading, the
// command is not run at all and the error says so.
//
// A result is written for every outcome the command reaches, one that could not
// be started included (ExitCode -1, Error set): the caller is blocked on the
// FIFO and has no other way to learn the run is over. The error is non-nil when
// the rendezvous or the report failed — ExecOutcome.Ran separates those two.
//
// ctx bounds the rendezvous and the command, but deliberately not the report:
// see writeExecResult.
func ExecPayload(
	ctx context.Context,
	resultFifoPath string,
	argv []string,
	opts ExecPayloadOptions,
) (ExecOutcome, error) {
	w, err := openResultWriter(ctx, resultFifoPath, opts.normalized())
	if err != nil {
		return ExecOutcome{Status: 1}, err
	}
	defer w.Close()

	result, status := runExecCommand(ctx, argv)
	return ExecOutcome{Result: result, Ran: true, Status: status},
		writeExecResult(w, result)
}

// ExecPayloadOptions configures ExecPayload.
type ExecPayloadOptions struct {
	// StartupTimeout bounds the wait for the caller's read end. Zero means 30s,
	// the same default CallExec uses; a caller that chose another value passes it
	// down in the payload argv, so the two halves give up together.
	StartupTimeout time.Duration
}

func (o ExecPayloadOptions) normalized() ExecPayloadOptions {
	o.StartupTimeout = cmp.Or(o.StartupTimeout, defaultExecStartupTimeout)
	return o
}

// openResultWriter waits for the caller's read end. O_NONBLOCK because the
// blocking form would wedge here forever when the caller is already gone, with
// the popup held open by a process nobody is waiting for; ENXIO is exactly the
// "no reader yet" case, so it is the one retried.
func openResultWriter(
	ctx context.Context,
	path string,
	opts ExecPayloadOptions,
) (*os.File, error) {
	deadline := time.Now().Add(opts.StartupTimeout)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.ENXIO) {
			return nil, fmt.Errorf("opening result fifo %q: %w", path, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"nothing is waiting for a result on %q after %v:"+
					" the process that opened this popup is gone, so the command was not run",
				path, opts.StartupTimeout,
			)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(execWriterRetryInterval):
		}
	}
}

func runExecCommand(ctx context.Context, argv []string) (ExecResult, int) {
	result := ExecResult{Command: argv, ExitCode: -1}
	if len(argv) == 0 {
		result.Error = "no command to run"
		return result, 1
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)

	err := cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			result.Error = err.Error()
			return result, 1
		}
	}
	result.ExitCode = cmd.ProcessState.ExitCode()
	return result, execProcessStatus(cmd.ProcessState)
}

// execProcessStatus turns a finished command's state into a status this process
// can exit with. A signal death is -1, which exit(2) cannot carry, so it becomes
// the 128+signal every shell reports; anything else unusable becomes 1. The
// reported ExecResult keeps the raw -1.
func execProcessStatus(state *os.ProcessState) int {
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return 1
}

// writeExecResult takes no context on purpose. By the time it runs the command
// is over and the result exists, so the thing a cancelled ctx would abort is the
// *reporting* of work that already happened — and cancellation is the ordinary
// case here, since a Ctrl-C in the popup hits the whole foreground process group
// and leaves this process's own ctx Done. A result larger than the pipe buffer
// cannot be handed over in one write, so an already-expired deadline would tear
// it in half and leave the caller with truncated JSON.
//
// The bound that remains is for a reader that stopped draining without going
// away; a caller that is simply gone fails immediately with EPIPE instead.
func writeExecResult(w *os.File, result ExecResult) error {
	b, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshaling exec result: %w", err)
	}

	if err := w.SetWriteDeadline(time.Now().Add(execResultWriteTimeout)); err != nil {
		return fmt.Errorf("bounding the result write on %q: %w", w.Name(), err)
	}

	// A caller that stopped reading gets EPIPE here rather than a blocked write:
	// the fifo is not this process's stdout or stderr, so Go turns the signal
	// into an error.
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("writing result fifo %q: %w", w.Name(), err)
	}
	// Closed here rather than left to the deferred close: the close is what gives
	// the reader its EOF, so a close that fails is a result that may never land.
	if err := w.Close(); err != nil {
		return fmt.Errorf("closing result fifo %q: %w", w.Name(), err)
	}
	return nil
}
