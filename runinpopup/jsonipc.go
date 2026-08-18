package runinpopup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// jsonIpcLauncherGrace is how long an exchange that has nothing to show gets
// after the popup launcher failed. The launcher's exit says little on its own:
// with tmux display-popup it is also how a successful run ends, carrying the
// payload's own status, while the floating-pane mechanisms exit long before the
// payload does. Ending the exchange the moment it errors would race a payload
// that is already opening its end, so a launcher failure only shortens the wait.
const jsonIpcLauncherGrace = 2 * time.Second

// JsonIpcLauncher opens a popup whose payload speaks JSON: In values reach it,
// Out values come back.
//
// Input travels one of two ways. Launch-time input is marshaled into the popup's
// command line by AddPayload, and the payload's stdin stays on the popup's
// terminal, where the user can type at it. Streamed input needs no AddPayload:
// without one a stdin FIFO is allocated instead, and the values handed to
// JsonIpcConn.Send travel over it as JSON lines.
//
// Output is always the payload's stdout, allocated a FIFO of its own and decoded
// into Out values as they arrive.
//
// A launcher is one-shot in the exec.Cmd sense: fill the fields in, call Exec
// once.
type JsonIpcLauncher[In, Out any] struct {
	// Popup opens the popup and supplies everything the exchange runs on: the
	// backend, the workspace holding the FIFOs, and the bound on the rendezvous
	// with the payload. Required.
	Popup *PopupLauncher
	// PartialSpec is the popup spec before the payload's input is in it.
	PartialSpec PopupSpec
	// AddPayload completes the spec, putting the marshaled launch-time input
	// wherever the payload expects to find it — the caller decides, since only it
	// knows the argv its payload answers to. nil means the exchange streams its
	// input instead: Exec's v is ignored and Send carries the values.
	AddPayload func(v In, spec PopupSpec) PopupSpec
}

// Exec launches the popup and hands back the exchange running in it.
//
// v is the launch-time input: it goes to AddPayload, which decides where in the
// popup's command line it belongs. Everything the payload then answers arrives
// on the returned connection.
func (l *JsonIpcLauncher[In, Out]) Exec(
	ctx context.Context,
	v In,
) (*JsonIpcConn[In, Out], error) {
	if l.Popup == nil {
		return nil, errors.New("JsonIpcLauncher.Popup must be set")
	}

	spec := l.PartialSpec
	// Only the streams the exchange actually uses are allocated: a stdin FIFO
	// would take the payload's stdin off the popup's terminal, which an exchange
	// that has nothing to send there still wants left alone.
	streams := PopupStreams{StdoutPipe: true}
	var send *io.PipeWriter
	if l.AddPayload != nil {
		spec = l.AddPayload(v, spec)
	} else {
		var recv *io.PipeReader
		recv, send = io.Pipe()
		streams.Stdin = recv
	}

	popup, err := l.Popup.Exec(ctx, spec, streams)
	if err != nil {
		return nil, err
	}
	stdout, _ := popup.StdoutPipe()

	conn := &JsonIpcConn[In, Out]{
		popup:        popup,
		send:         send,
		results:      make(chan Out),
		ended:        make(chan struct{}),
		launcherDone: make(chan struct{}),
		group:        new(errgroup.Group),
	}
	conn.group.Go(func() error {
		err := conn.finish(conn.stream(stdout))
		close(conn.ended)
		return err
	})
	conn.group.Go(conn.watchLauncher)
	return conn, nil
}

// JsonIpcConn is one JSON exchange with a popup payload: values out through
// Send, values back through Results, its end through Wait.
type JsonIpcConn[In, Out any] struct {
	popup *PopupCommand
	// send is the write half of the payload's stdin, nil for an exchange whose
	// input travelled in the popup's command line.
	send   *io.PipeWriter
	sendMu sync.Mutex

	results chan Out
	// ended is closed once the result stream has ended, whatever ended it. It is
	// what tells Wait the popup may be dismissed, and the launcher watch that it
	// has nothing left to shorten.
	ended chan struct{}
	// launcherErr is the launcher's own exit, published by closing launcherDone.
	launcherErr  error
	launcherDone chan struct{}
	group        *errgroup.Group

	mu      sync.Mutex
	decided bool
	outcome error
}

// Send JSON-encodes v onto the payload's stdin, as one line.
//
// It is only available to an exchange that streams its input — one whose
// launcher has no AddPayload. With one, the input travelled in the popup's
// command line and the payload's stdin was left on the popup's terminal, so
// there is nothing here to send on.
//
// A canceled send ends the input stream rather than merely returning: half a
// value has already reached the payload and cannot be taken back.
func (c *JsonIpcConn[In, Out]) Send(ctx context.Context, v In) error {
	if c.send == nil {
		return errors.New(
			"this exchange has no input stream:" +
				" its launcher's AddPayload put the input in the popup's command line," +
				" leaving the payload's stdin on the popup's terminal",
		)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshaling the popup payload's input: %w", err)
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	stop := context.AfterFunc(ctx, func() {
		_ = c.send.CloseWithError(context.Cause(ctx))
	})
	defer stop()

	if _, err := c.send.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("sending to the popup payload: %w", err)
	}
	return nil
}

// Results streams the values the payload writes on its stdout, decoded as they
// arrive, and is closed when the exchange ends. Every call returns the same
// channel.
//
// It has to be drained: the payload's output is relayed through it, so a popup
// nobody is taking values from ends up blocked on its own stdout, and Wait waits
// for a stream that has not ended.
func (c *JsonIpcConn[In, Out]) Results() <-chan Out {
	return c.results
}

// Wait waits for the exchange to end, dismisses the popup, gives back everything
// the launch took and reports the exchange's terminal error.
//
// The launcher's own exit status is not part of that error: with tmux
// display-popup the launcher carries the payload's status, so a command that
// exited non-zero would otherwise fail an exchange that went perfectly well. A
// launcher failure decides the exchange only while the output stream has not
// ended — in practice an exchange the payload never answered, though a dead
// popup can also cut short a stream that had already delivered values.
func (c *JsonIpcConn[In, Out]) Wait() error {
	<-c.ended
	c.popup.release()
	return c.group.Wait()
}

// stream decodes the payload's output until it ends, and reports how it ended.
func (c *JsonIpcConn[In, Out]) stream(r io.ReadCloser) error {
	// Closing the reader ends the launch's relay of a stream this end has stopped
	// taking, which is what a decoding failure leaves behind.
	defer r.Close()
	defer close(c.results)

	answered := false
	dec := json.NewDecoder(r)
	for {
		var v Out
		switch err := dec.Decode(&v); {
		case errors.Is(err, io.EOF):
			if answered {
				return nil
			}
			return c.launcherFailure()
		case err != nil:
			return fmt.Errorf("reading the popup payload's output: %w", err)
		}
		answered = true
		c.results <- v
	}
}

// launcherFailure hands an exchange the payload ended without a word whatever
// the launcher has to say about it: the two explain each other, since a popup
// that died before reaching its payload closes the payload's end exactly like a
// payload that closed it itself.
//
// The wait is bounded because a launcher may well outlive the exchange: the
// floating-pane mechanisms return early, and a popup still standing says nothing
// about a payload that has already let go.
func (c *JsonIpcConn[In, Out]) launcherFailure() error {
	select {
	case <-c.launcherDone:
		return c.launcherErr
	case <-time.After(jsonIpcLauncherGrace):
		return nil
	}
}

// watchLauncher ends a rendezvous the popup can no longer complete. A launcher
// that failed may have failed before the popup ever reached the payload, and
// nothing will open the payload's end after that — a wait only the launch's own
// startup bound would otherwise end, much later.
func (c *JsonIpcConn[In, Out]) watchLauncher() error {
	c.launcherErr = c.popup.waitLauncher()
	close(c.launcherDone)
	if c.launcherErr == nil {
		// A launcher that exits successfully says nothing: the floating-pane
		// mechanisms do exactly that while the payload still runs.
		return nil
	}
	select {
	case <-c.ended:
		return nil
	case <-time.After(jsonIpcLauncherGrace):
		err := c.finish(c.launcherErr)
		// Dismissing the popup is what ends the streams still waiting on it, so
		// the exchange ends here rather than at the startup bound.
		c.popup.release()
		return err
	}
}

// finish records how the exchange ended and reports the end it settles on. The
// first one recorded wins: once the result stream has said how the exchange went
// — a clean end included — a launcher failing on the way out cannot turn it into
// a failure, and the cancellation an ended rendezvous causes cannot replace the
// failure that ended it.
func (c *JsonIpcConn[In, Out]) finish(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.decided {
		c.decided, c.outcome = true, err
	}
	return c.outcome
}
