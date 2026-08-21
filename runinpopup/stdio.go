package runinpopup

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ngicks/go-common/iopipe"
	"golang.org/x/sync/errgroup"
)

// processStdio owns this process's standard streams for the duration of one
// exchange and hands out ends the exchange is allowed to close.
//
// The endpoints themselves are never closed here. gpg-agent speaks Assuan over
// this process's own stdin and stdout, so closing either would end the
// conversation for everyone, and a descriptor this process was handed by its
// parent is not this package's to close in the first place. What the exchange
// gets instead is a pipe in front of each endpoint: closing that pipe ends the
// relay, and the completion it reports afterwards says how the relay ended and
// how many bytes made it through.
type processStdio struct {
	// stdin, stdout and stderr are the derived ends the exchange relays through.
	stdin          io.ReadCloser
	stdout, stderr io.WriteCloser

	ends []derivedEnd
	// runners are the controller loops for the two output endpoints. The input
	// loop is not among them: see newProcessStdio.
	runners *errgroup.Group
	stop    context.CancelFunc
}

// derivedEnd is one pipe in front of an endpoint, and the channel carrying the
// single verdict on how that pipe ended.
type derivedEnd struct {
	name      string
	end       io.Closer
	completed <-chan error
}

// newProcessStdio starts the controllers relaying between the three endpoints
// and the ends the exchange uses. Canceling ctx ends every derived end with the
// cancellation as its cause, so a canceled exchange never waits on a stream.
// Call close when the exchange is over.
func newProcessStdio(
	ctx context.Context,
	stdin io.Reader,
	stdout, stderr io.Writer,
) (_ *processStdio, err error) {
	runCtx, stop := context.WithCancel(ctx)
	defer func() {
		if err != nil {
			stop()
		}
	}()

	s := &processStdio{runners: new(errgroup.Group), stop: stop}

	input := iopipe.NewReader(stdin)
	// Started rather than joined, and it cannot be otherwise: this loop spends the
	// exchange parked in a read on the OS input, which only the sender can end and
	// which this package must not close its way out of. Nothing of the exchange
	// waits on it — the exchange reads the derived end below, and closing that end
	// ends the exchange's use of the input whatever the loop is doing.
	go input.Run(runCtx)
	end, completed, err := input.Pipe(runCtx)
	if err != nil {
		return nil, fmt.Errorf("piping the assuan input: %w", err)
	}
	s.stdin = end
	s.ends = append(s.ends, derivedEnd{name: streamStdin, end: end, completed: completed})

	for _, o := range []struct {
		name string
		dst  io.Writer
		into *io.WriteCloser
	}{
		{streamStdout, stdout, &s.stdout},
		{streamStderr, stderr, &s.stderr},
	} {
		out := iopipe.NewWriter(o.dst)
		s.runners.Go(func() error { out.Run(runCtx); return nil })
		end, completed, err := out.Pipe(runCtx)
		if err != nil {
			return nil, fmt.Errorf("piping the %s: %w", o.name, err)
		}
		*o.into = end
		s.ends = append(s.ends, derivedEnd{name: o.name, end: end, completed: completed})
	}
	return s, nil
}

// close ends the exchange's use of the standard streams and reports the first
// relay that failed on its own.
//
// Closing a derived end is not a failure — it is how this package ends a relay —
// and neither is the cancellation that ends the exchange; both are caused by
// whatever is already being reported. An endpoint that stopped accepting bytes
// is worth reporting: what pinentry wrote past that point never reached
// gpg-agent.
func (s *processStdio) close() error {
	var failed error
	for _, e := range s.ends {
		_ = e.end.Close()
		// Received exactly once: the channel carries the single verdict on that
		// pipe, and nothing else is listening for it.
		if err := relayFailure(<-e.completed); err != nil && failed == nil {
			failed = fmt.Errorf("relaying the %s: %w", e.name, err)
		}
	}
	s.stop()
	// Joined so no loop is still writing to an endpoint after the exchange that
	// owns it has returned.
	_ = s.runners.Wait()
	return failed
}

func relayFailure(err error) error {
	closeErr, ok := errors.AsType[*iopipe.CloseError](err)
	if !ok {
		return err
	}
	switch {
	case errors.Is(closeErr.Cause, io.ErrClosedPipe),
		errors.Is(closeErr.Cause, context.Canceled),
		errors.Is(closeErr.Cause, context.DeadlineExceeded):
		return nil
	}
	return err
}
