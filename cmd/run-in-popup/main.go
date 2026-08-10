package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/ngicks/run-in-tmux-popup/cmd/run-in-popup/commands"
	"github.com/ngicks/run-in-tmux-popup/internal/cmdsignals"
)

func main() {
	blockOn, ctx, cancel := cmdsignals.NotifyContext(context.Background())

	var wg sync.WaitGroup
	wg.Go(blockOn)

	err := commands.Execute(ctx)

	// Check before cancel(nil) below — that call would set ctx.Err() and
	// manufacture a false positive.
	if err != nil && errors.Is(err, ctx.Err()) {
		if sigErr, ok := errors.AsType[*cmdsignals.SignalReceivedError](context.Cause(ctx)); ok {
			err = sigErr
		}
	}

	cancel(nil)
	wg.Wait()

	if err == nil {
		return
	}
	code, report := exitStatus(err)
	if report {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(code)
}

// exitStatus decides how the process ends for err: the status to exit with, and
// whether the error is worth a line on stderr.
//
// A leaf may ask for a status of its own — the exec payload has to exit the way
// the command it ran did, or the multiplexer reports the wrong thing. A bare
// request carries no message, since that command already had its say on the
// popup terminal; one wrapping an error still gets printed before we go.
func exitStatus(err error) (code int, report bool) {
	if coded, ok := errors.AsType[exitCoder](err); ok {
		return coded.ExitCode(), coded.Unwrap() != nil
	}
	return 1, true
}

// exitCoder is an error asking for a particular exit status. Declared here, at
// the consumer, so the commands package can keep the implementing type
// unexported.
type exitCoder interface {
	error
	ExitCode() int
	Unwrap() error
}
