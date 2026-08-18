package runinpopup

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// ExecPayload is the popup half of the exec exchange: it runs argv on the
// popup's terminal and reports the outcome as one JSON ExecResult on its own
// stdout, which the caller has wired to a FIFO of its own.
//
// stdin and stderr are left as the popup handed them over — the terminal — so
// the command can prompt on it and be watched live, and the command's stdout is
// teed there as well, since this process's own stdout is the result channel now.
// The tee needs pipes, so the command does not see a tty on its stdout: programs
// that gate color or progress rendering on isatty render plainly.
//
// The rendezvous is made before this half exists: the popup's command line
// redirects this process's stdout into the caller's FIFO, and opening a FIFO for
// writing is what blocks until the caller is reading it. What is left to find is
// a caller that arrived and then let go, which is what the startup check is for —
// a command whose result could never be reported is not run at all.
//
// A result is written for every outcome the command reaches, one that could not
// be started included (ExitCode -1, Error set): the caller is blocked on the
// FIFO and has no other way to learn the run is over. The error is non-nil when
// the startup check or the report failed — ExecOutcome.Ran separates those two.
//
// ctx bounds the command, but deliberately not the report: see writeExecResult.
func ExecPayload(
	ctx context.Context,
	argv []string,
	opts ExecPayloadOptions,
) (ExecOutcome, error) {
	w, err := openResultChannel()
	if err != nil {
		return ExecOutcome{Status: 1}, err
	}
	defer w.Close()

	if err := checkResultChannel(w); err != nil {
		return ExecOutcome{Status: 1}, err
	}

	result, status := runExecCommand(ctx, argv)
	return ExecOutcome{Result: result, Ran: true, Status: status},
		writeExecResult(w, result, opts.normalized().StartupTimeout)
}

// ExecPayloadOptions configures ExecPayload.
type ExecPayloadOptions struct {
	// StartupTimeout is this half's share of the bound the two halves give each
	// other. The rendezvous itself is over before this process starts — the
	// popup's command line opens the result channel, and that open is what waits
	// for the caller — so what is left to bound here is handing the finished
	// result to a caller that stopped taking it. Zero means 30s, the same default
	// the caller's own bound has; a caller that chose another value passes it
	// down in the payload argv, so the two halves give up together.
	StartupTimeout time.Duration
}

func (o ExecPayloadOptions) normalized() ExecPayloadOptions {
	o.StartupTimeout = cmp.Or(o.StartupTimeout, defaultPopupStartupTimeout)
	return o
}

// openResultChannel hands back this process's stdout — the caller's FIFO — as a
// descriptor of its own.
//
// The duplicate is what keeps a caller that let go from killing this process
// outright: Go turns EPIPE on descriptors 1 and 2 into the fatal SIGPIPE a shell
// pipeline expects, and the result channel needs it as an ordinary error
// instead, since the popup half has an outcome to report and a status to exit
// with even when nobody is left to take the result.
func openResultChannel() (*os.File, error) {
	fd, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		return nil, fmt.Errorf("duplicating stdout for the result channel: %w", err)
	}
	// The command runs on the popup's terminal and has no business holding the
	// caller's channel open: a child that inherited it would keep the caller
	// waiting for an end-of-file that already happened.
	syscall.CloseOnExec(fd)
	// Non-blocking so the report can carry a deadline — a caller that stopped
	// draining without letting go would otherwise block it forever. A stdout that
	// cannot be polled (a file, a terminal) simply keeps blocking writes, which
	// is why the failure is not fatal here.
	_ = syscall.SetNonblock(fd, true)
	return os.NewFile(uintptr(fd), "result channel"), nil
}

// checkResultChannel proves the caller is still there before the command runs.
//
// The write is a newline, which json.Decoder skips between values, so it costs
// the caller nothing; anything shorter would prove nothing, as a zero-length
// write to a pipe succeeds whether or not anyone is reading it. A caller that
// gave up on the rendezvous can still have left this end open — waking its own
// blocked open is what closes the last reader — and running a command whose
// result can never be reported is exactly what the two halves' shared bound
// exists to prevent.
func checkResultChannel(w *os.File) error {
	if _, err := w.Write([]byte("\n")); err != nil {
		return fmt.Errorf(
			"nothing is waiting for a result on this popup's stdout:"+
				" the process that opened this popup is gone, so the command was not run: %w",
			err,
		)
	}
	return nil
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
func writeExecResult(w *os.File, result ExecResult, timeout time.Duration) error {
	b, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshaling exec result: %w", err)
	}

	// A stdout that cannot be polled takes no deadline and needs none: only a
	// pipe can hold a write open on a reader that stopped reading.
	if err := w.SetWriteDeadline(time.Now().Add(timeout)); err != nil &&
		!errors.Is(err, os.ErrNoDeadline) {
		return fmt.Errorf("bounding the result write on %q: %w", w.Name(), err)
	}

	// A caller that stopped reading gets EPIPE here rather than a blocked write:
	// the result channel is a duplicate of stdout rather than stdout itself, so
	// Go turns the signal into an error.
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("writing the result channel: %w", err)
	}
	// Closed here rather than left to the deferred close: the close is what gives
	// the reader its EOF, so a close that fails is a result that may never land.
	if err := w.Close(); err != nil {
		return fmt.Errorf("closing the result channel: %w", err)
	}
	return nil
}
