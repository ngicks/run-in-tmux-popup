// Command zellij-popup-pinentry-curses proxies a pinentry prompt into a zellij
// floating pane.
//
// Deprecated: use "run-in-popup pinentry --backend zellij". This binary stays
// as a thin shim over runinpopup so existing gpg-agent wrapper scripts keep
// working.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ngicks/run-in-tmux-popup/internal/runworkspace"
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

const deprecationNotice = "zellij-popup-pinentry-curses is deprecated;" +
	` run "run-in-popup pinentry --backend zellij" instead.`

func main() {
	// stderr only: stdout carries the Assuan exchange with gpg-agent, and any
	// stray byte there breaks the protocol.
	fmt.Fprintln(os.Stderr, deprecationNotice)
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT,
	)
	defer stop()

	userData := runinpopup.ParsePinentryUserData(os.Getenv("PINENTRY_USER_DATA"))
	if userData.Path == "" || userData.SessionId == "" {
		return fmt.Errorf(
			"environment variable %q must be formatted as"+
				" \"ZELLIJ_POPUP:zellij_path:session_id:client_id\" but is %q",
			"PINENTRY_USER_DATA", os.Getenv("PINENTRY_USER_DATA"),
		)
	}

	backend, err := runinpopup.NewZellijBackend(runinpopup.BackendOptions{
		BinaryPath: userData.Path,
		SessionId:  userData.SessionId,
		Shell:      cmp.Or(os.Getenv("SHELL"), "bash"),
	})
	if err != nil {
		return err
	}

	workspace, err := runworkspace.Open(
		"zellij-popup-pinentry-curses-*",
		// TMUX_POPUP_DEBUG, not a zellij-specific name: this binary shared its
		// startup code with the tmux one and users' setups know that variable.
		userData.Debug() || os.Getenv("TMUX_POPUP_DEBUG") == "1",
		slog.New(slog.NewTextHandler(os.Stderr, nil)),
	)
	if err != nil {
		return err
	}
	defer workspace.Close()
	workspace.Logger.Info("PINENTRY_USER_DATA", slog.Any("data", userData))

	return runinpopup.CallPinentry(ctx, backend, runinpopup.PinentryOptions{
		Logger:       workspace.Logger,
		TempDir:      workspace.Dir,
		PinentryArgs: os.Args[1:],
	})
}
