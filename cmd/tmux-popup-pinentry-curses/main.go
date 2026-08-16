// Command tmux-popup-pinentry-curses proxies a pinentry prompt into a tmux
// display-popup.
//
// Deprecated: use "run-in-popup pinentry --backend tmux-popup". This binary
// stays as a thin shim over runinpopup so existing gpg-agent wrapper
// scripts keep working.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ngicks/run-in-tmux-popup/internal/runworkspace"
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/backend"
)

const deprecationNotice = "tmux-popup-pinentry-curses is deprecated;" +
	` run "run-in-popup pinentry --backend tmux-popup" instead.`

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
				" \"TMUX_POPUP:tmux_path:session_id:client_id\" but is %q",
			"PINENTRY_USER_DATA", os.Getenv("PINENTRY_USER_DATA"),
		)
	}

	popupBackend, err := backend.NewTmuxPopup(backend.Options{
		BinaryPath:  userData.Path,
		ClientId:    userData.ClientId,
		SessionMeta: userData.SessionMeta,
		TMUX:        os.Getenv("TMUX"),
	})
	if err != nil {
		return err
	}

	workspace, err := runworkspace.Open(
		"tmux-popup-pinentry-curses-*",
		userData.Debug() || os.Getenv("TMUX_POPUP_DEBUG") == "1",
		slog.New(slog.NewTextHandler(os.Stderr, nil)),
	)
	if err != nil {
		return err
	}
	defer workspace.Close()
	workspace.Logger.Info("PINENTRY_USER_DATA", slog.Any("data", userData))

	return runinpopup.CallPinentry(ctx, popupBackend, runinpopup.PinentryOptions{
		Logger:       workspace.Logger,
		TempDir:      workspace.Dir,
		PinentryArgs: os.Args[1:],
	})
}
