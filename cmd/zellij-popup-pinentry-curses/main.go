package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ngicks/run-in-tmux-popup/internal/popup"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tempdir, err := os.MkdirTemp("", "zellij-popup-pinentry-curses-*")
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = os.RemoveAll(tempdir)
	}()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if os.Getenv("TMUX_POPUP_DEBUG") == "1" {
		logFile, err := os.OpenFile(
			filepath.Join(tempdir, "log.txt"),
			os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o700,
		)
		if err != nil {
			panic(err)
		}
		defer logFile.Close()

		logger = slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	sigCh := make(chan os.Signal, 10)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)
	go func() {
		select {
		case <-ctx.Done():
			logger.Debug(
				"context canceled",
				slog.Any("err", ctx.Err()),
				slog.Any("cause", context.Cause(ctx)),
			)
		case sig := <-sigCh:
			cancel()
			logger.Debug(
				"signal received",
				slog.String("signal", sig.String()),
			)
		}
	}()

	shellName := cmp.Or(os.Getenv("SHELL"), "bash")
	zellijPath, sessionName, _ := strings.Cut(
		strings.TrimPrefix(
			strings.TrimSpace(
				os.Getenv("PINENTRY_USER_DATA"),
			),
			"ZELLIJ_POPUP:",
		),
		":",
	)
	if len(zellijPath) == 0 || len(sessionName) == 0 {
		panic(
			fmt.Errorf(
				"enviroment variable \"PINENTRY_USER_DATA\" must be"+
					" formated as \"ZELLIJ_POPUP:zellij_path:session_name\" but is %q",
				os.Getenv("PINENTRY_USER_DATA"),
			),
		)
	}

	err = popup.CallPinentry(
		ctx,
		logger,
		tempdir,
		func(ttyFifo, doneFifo string) (cmd string, args []string) {
			return zellijPath, []string{
				"--session=" + sessionName,
				"run",
				"--name=pinentry-curses",
				"--floating",
				"--close-on-exit",
				"--pinned=true",
				"--",
				shellName,
				"-c",
				fmt.Sprintf("echo $(tty) >> %s && read done < %s", ttyFifo, doneFifo),
			}
		},
		func(t string) (string, error) {
			return strings.TrimSpace(t), nil
		},
		"/usr/bin/pinentry-curses",
		os.Args[1:],
	)
	if err != nil {
		panic(err)
	}
}
