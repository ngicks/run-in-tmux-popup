// Package fifo creates and opens the named pipes this module's popups
// rendezvous over: the launch layer's payload streams, and a backend's own
// exchanges — zellij's environment delivery — that need the same open
// semantics.
package fifo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// openRetryInterval paces the wait for a payload's read end. Only the write
// side needs it: opening a FIFO for reading can block until the peer arrives,
// opening it for writing cannot without wedging on a peer that never comes.
const openRetryInterval = 20 * time.Millisecond

// Mkfifo creates a mode-0600 FIFO at path.
func Mkfifo(path string) error {
	if err := syscall.Mknod(path, syscall.S_IFIFO|0o600, 0); err != nil {
		return fmt.Errorf("creating fifo %q: %w", path, err)
	}
	return nil
}

// OpenReader opens the read end of a payload FIFO. The open blocks until the
// payload opens its write end, which is what makes it the rendezvous — and what
// means only ctx or startupTimeout can end it when the payload never arrives. A
// blocked open is woken by opening the same FIFO read-write, which succeeds with
// nobody on the other side.
func OpenReader(
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
		abort = startupError(path, startupTimeout)
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

// OpenWriter opens the write end of a payload FIFO. O_NONBLOCK because the
// blocking form would wedge here forever when the payload never reads; ENXIO is
// exactly the "no reader yet" case, so it is the one retried.
func OpenWriter(
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
			return nil, startupError(path, startupTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-time.After(openRetryInterval):
		}
	}
}

func startupError(path string, startupTimeout time.Duration) error {
	return fmt.Errorf(
		"the popup did not reach its payload within %v: nothing opened %q",
		startupTimeout, path,
	)
}
