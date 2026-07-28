// Package runinpopup implements the run-in-popup service backing the binary of
// the same name.
package runinpopup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
)

// RunOptions configures Run.
type RunOptions struct {
	// Logger receives progress logs. nil discards them.
	Logger *slog.Logger
	// Spec is the payload the popup runs.
	Spec PopupSpec
	// Stdout and Stderr receive the output of the popup *launcher* — the
	// tmux/zellij client process — not of the command inside the popup, which
	// owns the popup's terminal. nil discards it.
	Stdout io.Writer
	Stderr io.Writer
}

// Run opens a popup through backend, executes opts.Spec in it and waits for the
// popup launcher to exit. Canceling ctx interrupts the launcher.
//
// The launcher exits as soon as the multiplexer has taken over the popup, which
// for tmux display-popup is when the popup closes but for zellij is when the
// floating pane has been created. Callers that need to know when the payload
// itself finished must arrange for the payload to tell them, as the pinentry
// proxy does with its FIFO handshake.
func Run(ctx context.Context, backend Backend, opts RunOptions) error {
	logger := loggerOrDiscard(opts.Logger)
	return withPopupPrepared(ctx, logger, backend, func(ctx context.Context) error {
		popupCmd, err := startPopup(ctx, logger, backend, opts.Spec, opts.Stdout, opts.Stderr)
		if err != nil {
			return err
		}
		if err := popupCmd.Wait(); err != nil {
			return fmt.Errorf("popup failed: %w", err)
		}
		return nil
	})
}

// startPopup is the single popup-launching mechanism in this package: Run waits
// for the returned command, while the pinentry proxy leaves it running in the
// background and talks to the payload over its FIFOs.
func startPopup(
	ctx context.Context,
	logger *slog.Logger,
	backend Backend,
	spec PopupSpec,
	stdout, stderr io.Writer,
) (*exec.Cmd, error) {
	popupCmdPath, popupArgs := backend.PopupCommand(spec)

	logger.Debug(
		"popup cmd",
		slog.String("backend", backend.Name()),
		slog.String("path", popupCmdPath),
		slog.Any("args", popupArgs),
	)

	popupCmd := exec.CommandContext(ctx, popupCmdPath, popupArgs...)
	popupCmd.Cancel = func() error {
		return popupCmd.Process.Signal(syscall.SIGINT)
	}
	popupCmd.Stdout = stdout
	popupCmd.Stderr = stderr
	if extraEnv := backend.Environ(); len(extraEnv) > 0 {
		popupCmd.Env = append(os.Environ(), extraEnv...)
	}

	if err := popupCmd.Start(); err != nil {
		return nil, fmt.Errorf("popup failed: %w", err)
	}
	return popupCmd, nil
}

// withPopupPrepared runs fn between backend.Prepare and its restore func.
// Restore is best-effort (D8): it runs on a context detached from ctx so a
// timed-out or canceled popup still gets its multiplexer state back, and its
// error is logged instead of overriding fn's result.
func withPopupPrepared(
	ctx context.Context,
	logger *slog.Logger,
	backend Backend,
	fn func(context.Context) error,
) error {
	restore, err := backend.Prepare(ctx)
	if err != nil {
		return fmt.Errorf("backend %s: preparing popup: %w", backend.Name(), err)
	}
	if restore != nil {
		defer func() {
			if err := restore(context.WithoutCancel(ctx)); err != nil {
				logger.Warn(
					"restoring multiplexer state failed",
					slog.String("backend", backend.Name()),
					slog.Any("err", err),
				)
			}
		}()
	}
	return fn(ctx)
}

func loggerOrDiscard(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.DiscardHandler)
}
