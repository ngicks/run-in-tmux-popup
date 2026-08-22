package runinpopup

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ngicks/go-common/iopipe"
)

// assuanInput owns this process's standard input for the duration of one
// exchange and hands out an end the exchange is allowed to close.
//
// The endpoint itself is never closed here. gpg-agent speaks Assuan over this
// process's own stdin, so closing it would end the conversation for everyone,
// and a descriptor this process was handed by its parent is not this package's
// to close in the first place. What the exchange gets instead is a pipe in front
// of it: closing that pipe ends the relay, and the completion it reports
// afterwards says how the relay ended and how many bytes made it through.
//
// The output endpoints deliberately get nothing of the kind — they are handed to
// the pinentry child as they are, so what it answers gpg-agent with travels on
// the descriptor gpg-agent is reading rather than through a relay here. A relay
// would make this process the one writing to its own fd 1, and a gpg-agent that
// has said BYE may drop both ends of the connection without waiting to be
// acknowledged: the SIGPIPE such a write earns is fatal on fd 1 and 2, so the
// proxy would die before it could dismiss the popup, leaving it on screen with
// nobody left to close it.
type assuanInput struct {
	// end is the derived read end the exchange relays through.
	end io.ReadCloser
	// completed carries the single verdict on how that pipe ended.
	completed <-chan error
	stop      context.CancelFunc
}

// newAssuanInput starts the controller relaying between the input endpoint and
// the end the exchange uses. Canceling ctx ends that end with the cancellation as
// its cause, so a canceled exchange never waits on the stream. Call close when
// the exchange is over.
func newAssuanInput(ctx context.Context, stdin io.Reader) (*assuanInput, error) {
	runCtx, stop := context.WithCancel(ctx)

	input := iopipe.NewReader(stdin)
	// Started rather than joined, and it cannot be otherwise: this loop spends the
	// exchange parked in a read on the OS input, which only the sender can end and
	// which this package must not close its way out of. Nothing of the exchange
	// waits on it — the exchange reads the derived end below, and closing that end
	// ends the exchange's use of the input whatever the loop is doing.
	go input.Run(runCtx)
	end, completed, err := input.Pipe(runCtx)
	if err != nil {
		stop()
		return nil, fmt.Errorf("piping the assuan input: %w", err)
	}
	return &assuanInput{end: end, completed: completed, stop: stop}, nil
}

// close ends the exchange's use of the standard input and reports a relay that
// failed on its own.
//
// Closing the derived end is not a failure — it is how this package ends the
// relay — and neither is the cancellation that ends the exchange; both are caused
// by whatever is already being reported.
func (i *assuanInput) close() error {
	_ = i.end.Close()
	// Received exactly once: the channel carries the single verdict on that pipe,
	// and nothing else is listening for it.
	err := relayFailure(<-i.completed)
	i.stop()
	if err != nil {
		return fmt.Errorf("relaying the %s: %w", streamStdin, err)
	}
	return nil
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
