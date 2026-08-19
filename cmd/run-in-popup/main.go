package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/ngicks/run-in-tmux-popup/cmd/run-in-popup/commands"
	"github.com/ngicks/run-in-tmux-popup/internal/cmdsignals"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), cmdsignals.ExitSignals[:]...)

	err := commands.Execute(ctx)

	// Check before stop() below — that call would set ctx.Err() and
	// manufacture a false positive. On signal cancellation the cause names
	// the signal ("interrupt signal received") instead of the opaque
	// "context canceled".
	if err != nil && errors.Is(err, ctx.Err()) {
		err = context.Cause(ctx)
	}

	stop()

	if err == nil {
		return
	}
	// Every failure ends the same way: cobra silences what a leaf returns, so
	// this is where it is said, and no command asks for a status of its own —
	// what runs inside a popup keeps its status to itself.
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
