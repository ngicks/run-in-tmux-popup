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

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
