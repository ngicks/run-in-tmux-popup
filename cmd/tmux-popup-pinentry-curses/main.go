package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
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
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)
	defer stop()

	tempdir, err := os.MkdirTemp("", "tmux-popup-pinentry-curses-*")
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

	// Just little counter measurement for fifo hijack.
	// Adding random generated prefix and suffix to info
	// to detect suspicious sender
	var prefBytes, sufBytes [16]byte
	_, err = io.ReadFull(rand.Reader, prefBytes[:])
	if err != nil {
		panic(err)
	}
	_, err = io.ReadFull(rand.Reader, sufBytes[:])
	if err != nil {
		panic(err)
	}

	pref := hex.EncodeToString(prefBytes[:])
	suf := hex.EncodeToString(sufBytes[:])

	// TODO: do samething I've done to zellij version.

	err = popup.CallPinentry(
		ctx,
		logger,
		tempdir,
		func(ttyFifo, doneFifo string) (cmd string, args []string) {
			return "tmux", []string{
				"popup",
				"-e", "TTY_FIFO_FILE=" + ttyFifo,
				"-e", "DONE_FIFO_FILE=" + doneFifo,
				"-e", "SEC_PREFIX=" + pref,
				"-e", "SEC_SUFFIX=" + suf,
				"-E", "echo ${SEC_PREFIX}$(tty)${SEC_SUFFIX} >> ${TTY_FIFO_FILE} && read done < ${DONE_FIFO_FILE}",
			}
		},
		func(t string) (string, error) {
			t, ok := strings.CutPrefix(t, pref)
			if !ok {
				return "", fmt.Errorf("suspicious sender: incorrect prefix")
			}
			targetTty, ok := strings.CutSuffix(t, suf)
			if !ok {
				return "", fmt.Errorf("suspicious sender: incorrect suffix")
			}
			return targetTty, nil
		},
		"/usr/bin/pinentry-curses",
		os.Args[1:],
	)
	if err != nil {
		panic(err)
	}
}
